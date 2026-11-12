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
	if h.From != "PayPal <x@evil.test>" || h.To != "me@example.com" || h.Subject != "Invoice" {
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
