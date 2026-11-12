package worker

import (
	"strings"
	"testing"
)

func TestUniqueNameKeepsZipEntriesApart(t *testing.T) {
	used := map[string]bool{}
	got := []string{
		uniqueName(used, "invoice.pdf"),
		uniqueName(used, "invoice.pdf"),
		uniqueName(used, "INVOICE.PDF"), // same file on case-insensitive file systems
		uniqueName(used, "notes"),
		uniqueName(used, "notes"),
	}
	want := []string{"invoice.pdf", "invoice (1).pdf", "INVOICE (2).PDF", "notes", "notes (1)"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %q, want %q", got, want)
		}
	}
}

func TestMessageIDCannotCarryHeaderInjection(t *testing.T) {
	_, raw := ComposeRFC5322("me@example.com\r\nBcc: evil@example.com", "you@example.com", "hi", "body")
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("injected header line %q in:\n%s", line, head)
		}
	}
	if n := strings.Count(head, "\r\n"); n != 7 {
		t.Fatalf("header has %d line breaks, want 7:\n%s", n, head)
	}
}
