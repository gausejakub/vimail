package email

import (
	"regexp"
	"strings"
	"unicode"
)

// Everything in a received message is attacker-controlled. These helpers make
// such text safe to write to a terminal: without them a Subject, body,
// attachment name, or folder name can carry escape sequences that rewrite the
// screen, set the window title, or (OSC 52) overwrite the clipboard.

// reEscapeSeq matches complete terminal escape sequences so their parameter
// bytes are removed together with the introducer: CSI (ESC [ … final), OSC /
// DCS / APC / PM / SOS strings (terminated by BEL or ST), and two-byte ESC
// sequences. The 8-bit C1 introducers are covered too.
var reEscapeSeq = regexp.MustCompile(
	"(?:\x1b\\[|\u009b)[0-?]*[ -/]*[@-~]" + // CSI
		"|(?:\x1b[\\]PX^_]|[\u009d\u0090\u0098\u009e\u009f])[^\x07\x1b\u009c]*(?:\x07|\x1b\\\\|\u009c)?" + // OSC, DCS, SOS, PM, APC
		"|\x1b[ -/]*[0-~]") // other ESC sequences

// isUnsafeRune reports runes that must never reach the terminal: C0/C1
// controls, DEL, bidirectional overrides/isolates (used to disguise names
// such as "invoice‮fdp.exe"), and other invisible format characters.
func isUnsafeRune(r rune) bool {
	switch {
	case unicode.IsControl(r): // C0, DEL, C1
		return true
	case r >= 0x202A && r <= 0x202E, // bidi embeddings/overrides
		r >= 0x2066 && r <= 0x2069,            // bidi isolates
		r == 0x200E, r == 0x200F, r == 0x061C, // LRM, RLM, ALM
		r == 0x2028, r == 0x2029, // line/paragraph separators
		r == 0xFEFF,                // BOM / zero-width no-break space
		r >= 0xFFF9 && r <= 0xFFFB: // interlinear annotations
		return true
	}
	return false
}

// SanitizeLine makes untrusted text safe for single-line display (subjects,
// addresses, filenames, folder names, status labels). Escape sequences and
// control/bidi characters are removed; line breaks and tabs become spaces.
func SanitizeLine(s string) string {
	return sanitize(s, false)
}

// SanitizeText makes untrusted multi-line text (message bodies) safe for
// display. Newlines and tabs are kept; everything SanitizeLine removes is
// removed here too.
func SanitizeText(s string) string {
	return sanitize(s, true)
}

func sanitize(s string, multiline bool) string {
	clean := true
	for _, r := range s {
		if r == '\n' || r == '\t' {
			if !multiline {
				clean = false
				break
			}
			continue
		}
		if isUnsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	s = strings.ToValidUTF8(s, "�")
	s = reEscapeSeq.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			if multiline {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		case r == '\r':
			if !multiline {
				b.WriteByte(' ')
			}
		case isUnsafeRune(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
