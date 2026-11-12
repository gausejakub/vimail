package email

import (
	"strings"
	"testing"
)

func TestSanitizeLineStripsTerminalEscapes(t *testing.T) {
	cases := map[string]string{
		"plain subject": "plain subject",
		"clip\x1b]52;c;Y3VybCB4fHNoCg==\x07board": "clipboard",
		"st\x1b]0;title\x1b\\term":                "stterm",
		"unterminated\x1b]52;c;AAAA":              "unterminated",
		"clear\x1b[2J\x1b[31mred":                 "clearred",
		"alt\x1b[?1049lscreen":                    "altscreen",
		"c1\u009b2Jcsi":                           "c1csi",
		"c1\u009d0;x\u009cosc":                    "c1osc",
		"del\x7f bell\x07 nul\x00":                "del bell nul",
		"two\r\nlines\tand tab":                   "two  lines and tab",
		"invoice‮fdp.exe":                         "invoicefdp.exe",
		"iso⁦late⁩ mark‏":                         "isolate mark",
		"Příliš žluťoučký kůň 日本語 🎉":              "Příliš žluťoučký kůň 日本語 🎉",
	}
	for in, want := range cases {
		if got := SanitizeLine(in); got != want {
			t.Errorf("SanitizeLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeTextKeepsLayoutButNoControls(t *testing.T) {
	in := "line one\r\n\tindented\x1b]52;c;QUJD\x07\nline\x1b[2J three‮\x00"
	got := SanitizeText(in)
	if want := "line one\n\tindented\nline three"; got != want {
		t.Fatalf("SanitizeText = %q, want %q", got, want)
	}
	for _, r := range SanitizeText(strings.Repeat("\x1b[", 5000) + "\xff\xfe") {
		if r == 0x1b {
			t.Fatal("ESC survived")
		}
	}
}
