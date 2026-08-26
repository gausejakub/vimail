package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// setLimits overrides the rotation knobs for one test and restores them.
func setLimits(t *testing.T, size int64, age, interval time.Duration) {
	t.Helper()
	origSize, origAge, origInterval := maxLogSize, maxLogAge, retentionCheckInterval
	maxLogSize, maxLogAge, retentionCheckInterval = size, age, interval
	t.Cleanup(func() {
		// Stop any live drain goroutine before mutating the shared knobs.
		Close()
		maxLogSize, maxLogAge, retentionCheckInterval = origSize, origAge, origInterval
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestLiveSizeRotation(t *testing.T) {
	resetForTest(t)
	setLimits(t, 400, maxLogAge, time.Hour)
	dir := t.TempDir()
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Emit from several goroutines so rotation happens under concurrent load.
	var wg sync.WaitGroup
	const perG, goroutines = 50, 4
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				Info("rotate", fmt.Sprintf("goroutine %d entry %d with some padding text", g, i))
			}
		}(g)
	}
	wg.Wait()
	Close()

	active := logPath(dir)
	rotated := rotatedPath(dir)
	if _, err := os.Stat(rotated); err != nil {
		t.Fatalf("expected rotated file after exceeding size limit: %v", err)
	}
	info, err := os.Stat(active)
	if err != nil {
		t.Fatalf("active file missing after rotation: %v", err)
	}
	// The active file was rotated every time it crossed the limit, so it can
	// exceed maxLogSize by at most one entry.
	if info.Size() > maxLogSize+512 {
		t.Fatalf("active file grew past the rotation limit: %d bytes", info.Size())
	}

	for _, p := range []string{active, rotated} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Fatalf("%s has permissions %o, want 0600", p, perm)
		}
		// Every line must still be intact JSON despite rotation mid-stream.
		readEntries(t, p)
	}
}

func TestNoEntriesLostAcrossRotation(t *testing.T) {
	// Measure one encoded entry so the size limit can be pinned to "rotate
	// once, around the 6th of 8 entries" — only one previous file is kept, so
	// a second rotation would legitimately discard entries and break the count.
	resetForTest(t)
	probe := t.TempDir()
	if err := Init(probe, LevelDebug); err != nil {
		t.Fatalf("Init probe: %v", err)
	}
	Info("keep", "entry 0 with a fixed amount of padding text here")
	Close()
	info, err := os.Stat(logPath(probe))
	if err != nil || info.Size() == 0 {
		t.Fatalf("probe entry not written: %v", err)
	}
	entrySize := info.Size()

	setLimits(t, entrySize*6-1, maxLogAge, time.Hour)
	dir := t.TempDir()
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}
	const n = 8
	for i := 0; i < n; i++ {
		Info("keep", fmt.Sprintf("entry %d with a fixed amount of padding text here", i))
	}
	Close()

	total := len(readEntries(t, logPath(dir))) + len(readEntries(t, rotatedPath(dir)))
	if total != n {
		t.Fatalf("expected %d entries across active+rotated, got %d", n, total)
	}
}

func TestRetentionDeletesExpiredRotatedFile(t *testing.T) {
	resetForTest(t)
	setLimits(t, maxLogSize, 3*24*time.Hour, 20*time.Millisecond)
	dir := t.TempDir()

	// A rotated file whose newest entry is 4 days old: everything in it is
	// past retention. Note Init would prune it too; give the active file
	// content so the live ticker path is what must fire… so create it AFTER
	// Init instead.
	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}
	rotated := rotatedPath(dir)
	if err := os.WriteFile(rotated, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("write rotated: %v", err)
	}
	old := time.Now().Add(-4 * 24 * time.Hour)
	if err := os.Chtimes(rotated, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(rotated)
		return os.IsNotExist(err)
	}, "expired rotated file was not deleted by the live retention check")
	Close()
}

func TestLiveAgeRotationOfActiveFile(t *testing.T) {
	resetForTest(t)
	setLimits(t, maxLogSize, 3*24*time.Hour, 20*time.Millisecond)
	dir := t.TempDir()

	// Active file whose FIRST entry is 4 days old but whose mtime is recent —
	// exactly the long-running-session shape: init-time mtime checks keep it,
	// only oldest-entry age catches it.
	oldEntry := Entry{Time: time.Now().Add(-4 * 24 * time.Hour).Format(time.RFC3339Nano), Level: "info", Op: "old", Msg: "stale"}
	b, _ := json.Marshal(oldEntry)
	if err := os.WriteFile(logPath(dir), append(b, '\n'), 0600); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(rotatedPath(dir))
		return err == nil
	}, "active file with expired oldest entry was not rotated during the session")
	Close()

	// The stale entry moved to the rotated file; the active file starts fresh.
	rotatedEntries := readEntries(t, rotatedPath(dir))
	if len(rotatedEntries) != 1 || rotatedEntries[0].Op != "old" {
		t.Fatalf("rotated file should hold the stale entry, got %+v", rotatedEntries)
	}
}

func TestInitPrunesExpiredFiles(t *testing.T) {
	resetForTest(t)
	dir := t.TempDir()
	active, rotated := logPath(dir), rotatedPath(dir)
	for _, p := range []string{active, rotated} {
		if err := os.WriteFile(p, []byte("{}\n"), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		old := time.Now().Add(-4 * 24 * time.Hour)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Close()

	if _, err := os.Stat(rotated); !os.IsNotExist(err) {
		t.Fatal("expired rotated file survived Init")
	}
	entries := readEntries(t, active)
	for _, e := range entries {
		if e.Op == "" && e.Msg == "" && e.Time == "" {
			t.Fatal("expired active file content survived Init")
		}
	}
}

func TestOldestEntryTime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vimail.log")

	want := time.Now().Add(-48 * time.Hour).Truncate(time.Millisecond)
	e := Entry{Time: want.Format(time.RFC3339Nano), Level: "info", Op: "x", Msg: "y"}
	b, _ := json.Marshal(e)
	if err := os.WriteFile(p, append(b, '\n'), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := oldestEntryTime(p)
	if !got.Equal(want) {
		t.Fatalf("oldestEntryTime = %v, want %v", got, want)
	}

	// Garbage first line falls back to ModTime.
	if err := os.WriteFile(p, []byte("not json\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, _ := os.Stat(p)
	if got := oldestEntryTime(p); !got.Equal(info.ModTime()) {
		t.Fatalf("fallback = %v, want ModTime %v", got, info.ModTime())
	}
}
