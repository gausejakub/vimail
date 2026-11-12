package mcp

import (
	"testing"

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
