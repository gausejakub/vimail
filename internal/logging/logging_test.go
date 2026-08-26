package logging

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// resetForTest guarantees a clean global logger between tests.
func resetForTest(t *testing.T) {
	t.Helper()
	Close()
	t.Cleanup(Close)
}

// readEntries parses every JSON line in the given log file.
func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var entries []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("corrupt log line %q: %v", sc.Text(), err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan log: %v", err)
	}
	return entries
}

func TestConcurrentEmitAndSetLevel(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					Info("test", "emit during level change")
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		SetLevel(Level(i % 4))
	}
	close(stop)
	wg.Wait()
}

func TestConcurrentEmitAndClose(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					Debug("test", "emit during close")
				}
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	Close() // must not panic with emitters still running
	close(stop)
	wg.Wait()

	// Logger must be reusable after Close.
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("re-Init after Close: %v", err)
	}
	Info("test", "after reinit")
	Close()

	entries := readEntries(t, filepath.Join(dir, "vimail.log"))
	found := false
	for _, e := range entries {
		if e.Msg == "after reinit" {
			found = true
		}
	}
	if !found {
		t.Fatal("entry emitted after re-Init was not written")
	}
}

func TestInitCloseCycles(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := Init(dir, LevelDebug); err != nil {
			t.Fatalf("Init cycle %d: %v", i, err)
		}
		Info("cycle", "entry")
		Close()
	}
	entries := readEntries(t, filepath.Join(dir, "vimail.log"))
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries across cycles, got %d", len(entries))
	}
}

func TestDoubleInitAndDoubleClose(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("second Init should be a no-op, got: %v", err)
	}
	Close()
	Close() // second Close must be a safe no-op
}

func TestQueueFullDoesNotBlock(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()

	// Shrink the queue and stall it: with no drain goroutine consuming, a
	// second emit can only return if the non-blocking drop path works.
	orig := chanSize
	chanSize = 1
	defer func() { chanSize = orig }()

	f, err := os.OpenFile(filepath.Join(dir, "vimail.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	l := &Logger{ch: make(chan Entry, chanSize), file: f, done: make(chan struct{})}
	l.level.Store(int32(LevelDebug))
	defaultLogger.Store(l)
	defer func() {
		defaultLogger.Store(nil)
		f.Close()
	}()

	returned := make(chan struct{})
	go func() {
		Info("test", "fills queue")
		Info("test", "must be dropped, not block")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked on a full queue")
	}
}

func TestCloseFlushesAcceptedEntries(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}
	const n = 500
	for i := 0; i < n; i++ {
		Info("flush", "entry")
	}
	Close()

	entries := readEntries(t, filepath.Join(dir, "vimail.log"))
	if len(entries) != n {
		t.Fatalf("expected all %d accepted entries flushed on Close, got %d", n, len(entries))
	}
}

func TestLevelFiltering(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()
	if err := Init(dir, LevelWarn); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Debug("test", "below level")
	Warn("test", "at level")
	SetLevel(LevelDebug)
	Debug("test", "after lowering")
	Close()

	entries := readEntries(t, filepath.Join(dir, "vimail.log"))
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Msg != "at level" || entries[1].Msg != "after lowering" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}
