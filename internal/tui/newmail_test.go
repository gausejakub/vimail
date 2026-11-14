package tui

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
)

func TestNewMailNotifications(t *testing.T) {
	db, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := cache.NewSQLiteStore(db)
	for _, acct := range []string{"me@x.test", "other@x.test"} {
		if err := store.SeedAccount("", acct, "imap.x.test", 993, "", 0); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"Inbox", "Newsletters"} {
			store.EnsureFolder(acct, f)
		}
	}
	put := func(acct, folder string, uid uint32, from, subject string, unread bool) {
		t.Helper()
		if err := store.UpsertMessage(acct, folder, email.Message{UID: uid, From: from, Subject: subject, Unread: unread, Date: time.Now().Add(time.Duration(uid) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	type sent struct{ title, body string }
	var got []sent
	old := sendNotification
	sendNotification = func(_ config.NotificationsConfig, title, body string) error {
		got = append(got, sent{title, body})
		return nil
	}
	defer func() { sendNotification = old }()
	fire := func(m Model) {
		if cmd := m.newMailCmd(); cmd != nil {
			cmd()
		}
	}

	cfg := config.DefaultConfig()
	m := New(cfg, store)
	put("me@x.test", "Inbox", 1, "Old <old@x.test>", "already here", true)

	// Disabled: nothing, ever.
	fire(m)
	if len(got) != 0 {
		t.Fatalf("notified while disabled: %v", got)
	}

	cfg.Notify.Enabled = true
	m = New(cfg, store)
	fire(m) // first look: mail already present does not notify
	if len(got) != 0 {
		t.Fatalf("notified for mail present at start: %v", got)
	}

	put("me@x.test", "Inbox", 2, "Petr Trefil <petr@partner.test>", "Digifest", true)
	put("me@x.test", "Inbox", 3, "Read <read@x.test>", "read elsewhere", false)                        // already read
	put("me@x.test", "Newsletters", 9, "News <n@x.test>", "weekly", true)                              // folder not watched
	put("me@x.test", "Inbox", cache.PlaceholderUIDBase+5, "Moved <m@x.test>", "optimistic move", true) // placeholder row
	fire(m)
	if len(got) != 1 || got[0].title != "Petr Trefil" || got[0].body != "Digifest" {
		t.Fatalf("notifications = %+v", got)
	}
	fire(m) // nothing new: no repeat
	if len(got) != 1 {
		t.Fatalf("repeated notification: %+v", got)
	}

	// Several at once become one notification per account.
	put("me@x.test", "Inbox", 4, "a@x.test", "one", true)
	put("me@x.test", "Inbox", 5, "b@x.test", "two", true)
	fire(m) // first look at other@x.test happened above, so its mail counts now
	put("other@x.test", "Inbox", 1, "c@x.test", "elsewhere", true)
	fire(m)
	if len(got) != 3 || got[1].title != "2 new messages in me@x.test" || got[2].title != "c@x.test" {
		t.Fatalf("notifications = %+v", got)
	}

	// Subjects hidden, and only chosen accounts.
	cfg.Notify.HideSubject, cfg.Notify.Accounts = true, []string{"other@x.test"}
	m = New(cfg, store)
	fire(m)
	put("me@x.test", "Inbox", 6, "x@x.test", "secret", true)
	put("other@x.test", "Inbox", 2, "Boss <boss@x.test>", "confidential", true)
	got = nil
	fire(m)
	if len(got) != 1 || got[0].title != "Boss" || got[0].body != "New mail in other@x.test" {
		t.Fatalf("filtered notifications = %+v", got)
	}
}
