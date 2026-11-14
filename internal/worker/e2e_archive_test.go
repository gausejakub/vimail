package worker_test

import (
	"fmt"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/gausejakub/vimail/internal/cache"
)

func (env *e2eEnv) createMailbox(t *testing.T, user, name string) {
	t.Helper()
	if err := env.imap.client(t, user).Create(name, nil).Wait(); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

func (env *e2eEnv) seedInbox(t *testing.T, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		env.imap.appendRaw(t, me, "INBOX", []byte(fmt.Sprintf(
			"From: a@partner.test\r\nTo: %s\r\nSubject: msg %d\r\nMessage-ID: <m%d@partner.test>\r\n\r\nbody\r\n", me, i, i)), imap.FlagSeen)
	}
}

// Archiving a selection moves it to the Archive folder on the server and in
// the cache.
func TestArchiveMovesToArchiveFolder(t *testing.T) {
	env := newE2E(t)
	env.createMailbox(t, me, "Archive")
	env.seedInbox(t, 3)
	env.sync(t, me)

	inbox := env.store.MessagesFor(me, "Inbox")
	if len(inbox) != 3 {
		t.Fatalf("inbox = %d", len(inbox))
	}
	res := env.coord.ArchiveMessagesNow(me, "Inbox", []uint32{inbox[0].UID, inbox[1].UID})
	if res.Err != nil || !res.Delivered {
		t.Fatalf("archive: %+v", res)
	}
	if n := len(env.imap.messages(t, me, "Archive")); n != 2 {
		t.Fatalf("%d messages in server Archive, want 2", n)
	}
	if n := len(env.imap.messages(t, me, "INBOX")); n != 1 {
		t.Fatalf("%d messages left in server INBOX, want 1", n)
	}
	if in, ar := len(env.store.MessagesFor(me, "Inbox")), len(env.store.MessagesFor(me, "Archive")); in != 1 || ar != 2 {
		t.Fatalf("cache: inbox=%d archive=%d", in, ar)
	}
	// Archiving from Archive, or drafts and trash, is refused.
	for _, folder := range []string{"Archive", "Drafts", "Trash"} {
		if res := env.coord.ArchiveMessagesNow(me, folder, []uint32{1}); res.Err == nil {
			t.Errorf("archiving from %s accepted", folder)
		}
	}
}

// A Gmail-style account has no Archive folder: archiving moves the message
// to All Mail, which is not cached, so it only disappears from the folder.
func TestArchiveToAllMailWithoutCachingIt(t *testing.T) {
	env := newE2E(t)
	env.cfg.Accounts[0].SkipFolders = []string{"All Mail"}
	env.rebuildCoordinator(t)
	env.createMailbox(t, me, "All Mail")
	env.seedInbox(t, 2)
	env.sync(t, me)
	for _, f := range env.store.FoldersFor(me) {
		if f.Name == "All Mail" {
			t.Fatal("All Mail is cached; the test needs it skipped")
		}
	}
	inbox := env.store.MessagesFor(me, "Inbox")
	res := env.coord.ArchiveMessagesNow(me, "Inbox", []uint32{inbox[0].UID})
	if res.Err != nil || !res.Delivered {
		t.Fatalf("archive: %+v", res)
	}
	if n := len(env.imap.messages(t, me, "All Mail")); n != 1 {
		t.Fatalf("%d messages in server All Mail, want 1", n)
	}
	if n := len(env.imap.messages(t, me, "INBOX")); n != 1 {
		t.Fatalf("%d messages left in server INBOX, want 1", n)
	}
	if n := len(env.store.MessagesFor(me, "Inbox")); n != 1 {
		t.Fatalf("cache inbox = %d, want 1", n)
	}
	for _, op := range env.store.RecentOps(10) {
		if op.Type == cache.OpMove && op.Status != cache.OpCompleted {
			t.Fatalf("move op %s: %s", op.Status, op.Error)
		}
	}
}

// Without an Archive or All Mail folder there is nowhere to archive to.
func TestArchiveNeedsAFolder(t *testing.T) {
	env := newE2E(t)
	env.seedInbox(t, 1)
	env.sync(t, me)
	uid := env.store.MessagesFor(me, "Inbox")[0].UID
	if res := env.coord.ArchiveMessagesNow(me, "Inbox", []uint32{uid}); res.Err == nil {
		t.Fatal("archived without an Archive folder")
	}
	if n := len(env.store.MessagesFor(me, "Inbox")); n != 1 {
		t.Fatalf("message removed although nothing was archived: %d left", n)
	}
}
