package worker

import (
	"reflect"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestAssignFolderNames(t *testing.T) {
	listed := []listedMailbox{
		{name: "INBOX"},
		{name: "[Gmail]", noSelect: true},
		{name: "[Gmail]/Gesendet", role: roleDisplayName(imap.MailboxAttrSent)},
		{name: "[Gmail]/Entwürfe", role: roleDisplayName(imap.MailboxAttrDrafts)},
		{name: "[Gmail]/Papierkorb", role: roleDisplayName(imap.MailboxAttrTrash)},
		{name: "[Gmail]/Alle Nachrichten", role: roleDisplayName(imap.MailboxAttrAll)},
		{name: "Sent"},       // a user folder that happens to be called Sent
		{name: "Rechnungen"}, // ordinary folder
		{name: "Odoslané"},   // unrecognised name, set by config
		{name: "Junk"},       // recognised by name
		{name: "a\x1b[2Jb"},  // sanitized
		{name: "a\x1b[1mb"},  // sanitizes to the same text
	}
	got := assignFolderNames(listed, map[string]string{"archive": "Odoslané", "bogus": "INBOX", "trash": "missing"})
	want := map[string]string{
		"INBOX":                    "Inbox",
		"[Gmail]/Gesendet":         "Sent",
		"[Gmail]/Entwürfe":         "Drafts",
		"[Gmail]/Papierkorb":       "Trash",
		"[Gmail]/Alle Nachrichten": "All Mail",
		"Sent":                     "Sent (2)", // the role folder keeps "Sent"
		"Rechnungen":               "Rechnungen",
		"Odoslané":                 "Archive",
		"Junk":                     "Spam",
		"a\x1b[2Jb":                "ab",
		"a\x1b[1mb":                "ab (2)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assignFolderNames:\n got %v\nwant %v", got, want)
	}
	// Display names are unique, so two server folders never share a cache folder.
	seen := map[string]string{}
	for mailbox, name := range got {
		if other, dup := seen[name]; dup {
			t.Fatalf("%q and %q both display as %q", mailbox, other, name)
		}
		seen[name] = mailbox
	}
}
