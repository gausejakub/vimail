package tui

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
)

// reSGR matches only the colour/style sequences the renderer itself emits.
// Anything else that starts with ESC came from message content.
var reSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// fuzzKeys is the alphabet the fuzzer may press. It covers navigation, the
// reader, visual mode, search and compose, and leaves out quitting.
var fuzzKeys = []string{"j", "k", "l", "h", "tab", "shift+tab", "enter", "esc", "v", "V", "G", "g", "/", "?", "r", "c", "f", "U", "y", "w", "b", "$", "0", "ctrl+d", "ctrl+u", "d", "x", "i", "a", "o", "n", "1", "9", " ", "up", "down"}

// FuzzAppRender puts hostile data straight into the cache — bypassing the
// sanitizing done when mail is fetched, as mail cached by an older version
// would — and drives the whole TUI. Whatever is stored, no frame may carry a
// control character or escape sequence from it, and nothing may panic or hang.
func FuzzAppRender(f *testing.F) {
	f.Add("Inbox", "Alice <a@example.com>", "Hello", "body text\nline 2", "", []byte{0, 2, 6, 8})
	f.Add("In\x1b[2Jbox", "Pay‮Pal <x@evil.test>", "Inv\x1b]52;c;QUJD\x07oice", "a\x1b[31mb\x07\u009b0m", "<p>x</p>", []byte{2, 6, 0, 0, 12, 7})
	f.Add("Šablony 🗂", "名前 <n@example.jp>", "日本語 👩‍💻 é", "{\"a\":[1,{\"b\":2}]}", "", []byte{5, 5, 1, 9, 9, 7, 3})
	f.Add("", "", "", "", "<img src=x>", []byte{6, 6, 6, 7, 7})
	f.Add("Drafts", "x", "y", "draft body", "", []byte{6, 18, 19, 7, 7})
	f.Fuzz(func(t *testing.T, folder, from, subject, body, htmlBody string, keys []byte) {
		if len(folder) > 200 || len(from) > 500 || len(subject) > 500 || len(body) > 8000 || len(htmlBody) > 8000 || len(keys) > 40 {
			return
		}
		// Watchdog for hangs anywhere in this function, including the deferred
		// Close: write every goroutine's stack where VIMAIL_FUZZ_DUMP points
		// and abort the worker.
		if dump := os.Getenv("VIMAIL_FUZZ_DUMP"); dump != "" {
			wd := time.AfterFunc(20*time.Second, func() {
				buf := make([]byte, 1<<20)
				buf = buf[:runtime.Stack(buf, true)]
				_ = os.WriteFile(dump, buf, 0600)
				os.Exit(3)
			})
			defer wd.Stop()
		}
		db, err := cache.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		store := cache.NewSQLiteStore(db)
		const account = "alice@example.com"
		if err := store.SeedAccount("Alice", account, "", 993, "", 587); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Inbox", folder} {
			if name == "" {
				continue
			}
			if _, err := store.EnsureFolder(account, name); err != nil {
				return // the store refused the name
			}
			for uid := uint32(1); uid <= 3; uid++ {
				_ = store.UpsertMessage(account, name, email.Message{
					UID: uid, MessageID: name + string(rune('a'+uid)), From: from, To: from, Subject: subject,
					Body: body, HTMLBody: htmlBody, Unread: uid == 1,
					Date:        time.Date(2026, 11, 1, 12, int(uid), 0, 0, time.UTC),
					Attachments: []email.Attachment{{Filename: subject, Size: 3}},
				})
			}
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic: %v\n%s", r, debug.Stack())
				}
			}()
			for _, size := range [][2]int{{120, 40}, {40, 12}, {8, 3}} {
				cfg := config.DefaultConfig()
				cfg.AI = config.AIConfig{} // never run an agent CLI from a fuzz input
				m := New(cfg, store)
				sendMsg(&m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				drainCmds(&m, m.Init())
				checkFrame(t, m.View())
				for _, k := range keys {
					send(&m, fuzzKeys[int(k)%len(fuzzKeys)])
					checkFrame(t, m.View())
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			// Panic rather than Fatalf: the deferred db.Close would wait for the
			// stuck goroutine. The dump shows where it is stuck.
			buf := make([]byte, 1<<18)
			buf = buf[:runtime.Stack(buf, true)]
			panic(fmt.Sprintf("TUI hung: folder=%q from=%q subject=%q body=%q html=%q keys=%v\n%s", folder, from, subject, body, htmlBody, keys, buf))
		}
	})
}

func checkFrame(t *testing.T, view string) {
	t.Helper()
	plain := reSGR.ReplaceAllString(view, "")
	for _, r := range plain {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			t.Fatalf("character %U from stored data reached the screen:\n%q", r, plain)
		}
	}
}
