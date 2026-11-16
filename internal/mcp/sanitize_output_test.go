package mcp

import (
	"testing"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
)

// Mail cached before a sanitizing rule existed must still be clean when it is
// handed to an AI client.
func TestHeaderSanitizesCachedFields(t *testing.T) {
	hidden := string(rune(0xE0049)) + string(rune(0xE0047)) // tag characters
	h := header(email.Message{
		From:    "Pay‮Pal <x@evil.test>",
		To:      "me@example.com\x1b]52;c;QUJD\x07",
		Subject: "Invoice" + hidden,
	}, false)
	if h.From != "PayPal <x@evil.test>" || len(h.To) != 1 || h.To[0] != "me@example.com" || h.Subject != "Invoice" {
		t.Fatalf("header = %+v", h)
	}
}

// Folder names and error text in the operation log can quote the mail server;
// they must reach the client without escape sequences or hidden characters.
func TestListOperationsSanitizesFolderAndError(t *testing.T) {
	store := seededStore(t)
	hidden := string(rune(0xE0049)) + string(rune(0xE0047))
	id, err := store.QueueOp(cache.OpMarkRead, testAcct, "In\x1b[2Jbox"+hidden, cache.MarkReadPayload{UIDs: []uint32{1}})
	if err != nil {
		t.Fatal(err)
	}
	if !store.StartOp(id) {
		t.Fatal("claim op")
	}
	store.FailOp(id, "550 rejected\x1b]52;c;QUJD\x07 by‮ server"+hidden)

	var out listOperationsResult
	call(t, connect(t, store), "list_operations", nil, &out)
	if len(out.Operations) != 1 {
		t.Fatalf("operations = %+v", out.Operations)
	}
	op := out.Operations[0]
	if op.Folder != "Inbox" || op.Error != "550 rejected by server" {
		t.Fatalf("folder=%q error=%q", op.Folder, op.Error)
	}
}

// A draft saved by an AI client is stored clean, so the TUI never has to
// trust it. A recipient carrying a header injection is rejected outright.
func TestSaveDraftSanitizesArguments(t *testing.T) {
	store := seededStore(t)
	session := connect(t, store)
	if !callErr(t, session, "save_draft", map[string]any{"to": "bob@example.com\r\nBcc: evil@example.com"}) {
		t.Fatal("save_draft accepted a recipient with an injected header")
	}
	var out saveDraftResult
	call(t, session, "save_draft", map[string]any{
		"to": "bob@example.com", "subject": "Hi\x1b[2J",
		"body": "hello\x1b]52;c;QUJD\x07\nworld",
	}, &out)
	d, ok := store.Draft(testAcct, out.ID)
	if !ok {
		t.Fatal("draft not stored")
	}
	if len(d.To) != 1 || d.To[0] != "bob@example.com" || len(d.Bcc) != 0 || d.Subject != "Hi" || d.Body != "hello\nworld" {
		t.Fatalf("stored draft to=%q bcc=%q subject=%q body=%q", d.To, d.Bcc, d.Subject, d.Body)
	}
	if len(store.Drafts(testAcct)) != 1 {
		t.Fatalf("rejected call stored a draft: %d drafts", len(store.Drafts(testAcct)))
	}
}

// A client that asks its user before destructive calls must ask for these:
// a move can target Trash or Spam, and marking everything read is not undone.
func TestMoveAndMarkAllReadAreDestructive(t *testing.T) {
	for _, name := range []string{"move_message", "mark_all_read", "delete_message", "send_email"} {
		if toolKinds[name] != toolDestructive {
			t.Errorf("%s is not classified destructive", name)
		}
	}
}

// read_message is bounded like read_messages: text beyond what the reader
// sees in the preview must not reach the client unannounced.
func TestBodyIsBoundedAndKeepsItsFullLength(t *testing.T) {
	long := make([]rune, maxBodyChars+500)
	for i := range long {
		long[i] = 'a'
	}
	m := readMessageResult{Body: string(long)}
	truncateBody(&m, maxBodyChars)
	if !m.BodyTruncated || len([]rune(m.Body)) != maxBodyChars || m.BodyChars != maxBodyChars+500 {
		t.Fatalf("truncated=%v len=%d chars=%d", m.BodyTruncated, len([]rune(m.Body)), m.BodyChars)
	}
	truncateBody(&m, 100) // a caller's smaller limit, applied afterwards
	if len([]rune(m.Body)) != 100 || m.BodyChars != maxBodyChars+500 {
		t.Fatalf("second limit: len=%d chars=%d", len([]rune(m.Body)), m.BodyChars)
	}
}

// Characters the preview does not draw must not reach an AI client either:
// text can be encoded in zero-width characters and variation selectors.
func TestOutputDropsZeroWidthCharacters(t *testing.T) {
	h := header(email.Message{Subject: "pay​‌‍­️͏ㅤ now"}, false)
	if h.Subject != "pay now" {
		t.Fatalf("subject = %+q", h.Subject)
	}
}
