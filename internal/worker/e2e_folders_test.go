package worker_test

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/worker"
)

// An account whose special folders have names vimail does not know (a German
// mailbox) but advertises their roles: drafts must be uploaded to its Drafts
// folder, sent mail filed in its Sent folder, and deleting must move to its
// Trash, all addressed by vimail's own names.
func TestLocalizedFoldersAreFoundByRole(t *testing.T) {
	testMailboxes = []testMailbox{
		{name: "INBOX"},
		{name: "Gesendet", use: imap.MailboxAttrSent},
		{name: "Entwürfe", use: imap.MailboxAttrDrafts},
		{name: "Papierkorb", use: imap.MailboxAttrTrash},
		{name: "Werbung", use: imap.MailboxAttrJunk},
		{name: "Rechnungen"},
	}
	t.Cleanup(func() { testMailboxes = defaultMailboxes })
	env := newE2E(t)
	env.imap.appendRaw(t, me, "INBOX", []byte("From: a@partner.test\r\nTo: "+me+"\r\nSubject: hello\r\nMessage-ID: <m1@partner.test>\r\n\r\nbody\r\n"))
	env.sync(t, me)

	var names []string
	for _, f := range env.store.FoldersFor(me) {
		names = append(names, f.Name)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"Inbox", "Sent", "Drafts", "Trash", "Spam", "Rechnungen"} {
		if !strings.Contains(","+got+",", ","+want+",") {
			t.Errorf("folder %q missing from %s", want, got)
		}
	}
	for _, raw := range []string{"Gesendet", "Entwürfe", "Papierkorb", "Werbung"} {
		if strings.Contains(got, raw) {
			t.Errorf("raw folder name %q shown in %s", raw, got)
		}
	}

	// Draft upload goes to the folder with the \Drafts role.
	if err := env.store.SaveDraft(me, email.Draft{ID: "d1", To: []string{partner}, Subject: "entwurf", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := env.coord.FlushDraftOpsNow(me); err != nil {
		t.Fatalf("draft upload: %v", err)
	}
	if n := len(env.imap.messages(t, me, "Entwürfe")); n != 1 {
		t.Fatalf("%d messages in the server Drafts folder, want 1", n)
	}

	// Sending files the copy in the folder with the \Sent role.
	if res := env.coord.SendNow(me, worker.SendRequest{ToList: []string{partner}, Subject: "s", Body: "b"}); res.Err != nil {
		t.Fatalf("send: %v", res.Err)
	}
	if n := len(env.imap.messages(t, me, "Gesendet")); n != 1 {
		t.Fatalf("%d messages in the server Sent folder, want 1", n)
	}

	// Deleting moves to the folder with the \Trash role.
	inbox := env.store.MessagesFor(me, "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("inbox has %d messages", len(inbox))
	}
	env.coord.DeleteMessage(me, "Inbox", inbox[0].UID)()
	if n := len(env.imap.messages(t, me, "Papierkorb")); n != 1 {
		t.Fatalf("%d messages in the server Trash folder, want 1", n)
	}
	if n := len(env.imap.messages(t, me, "INBOX")); n != 0 {
		t.Fatalf("%d messages left in INBOX", n)
	}
}
