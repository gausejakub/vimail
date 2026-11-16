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
		r >= 0xFFF9 && r <= 0xFFFB, // interlinear annotations
		r == 0x180E,                // Mongolian vowel separator (invisible)
		r >= 0x2060 && r <= 0x2064, // word joiner, invisible operators
		r >= 0x206A && r <= 0x206F, // deprecated format controls
		// Zero-width and blank characters. The preview never drew them, so
		// the reader could not see text encoded in them (as bits, in runs of
		// zero-width space and joiners, or as nibbles, in the sixteen
		// variation selectors) while an AI client received it.
		r == 0x200B,                                        // zero-width space (the joiners: see joinerAllowed)
		r == 0x00AD,                                        // soft hyphen
		r == 0x034F,                                        // combining grapheme joiner
		r >= 0xFE00 && r <= 0xFE0D,                         // variation selectors 1-14 (15 and 16: see joinerAllowed)
		r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0, // Hangul fillers (blank)
		r == 0x17B4, r == 0x17B5, // Khmer inherent vowels (invisible)
		r >= 0x1D173 && r <= 0x1D17A, // musical format controls
		// Unicode tag characters and the variation-selector supplement are
		// invisible on screen but readable by an AI client: text hidden in
		// them is a prompt injection the user cannot see ("ASCII smuggling").
		r >= 0xE0000 && r <= 0xE007F,
		r >= 0xE0100 && r <= 0xE01EF:
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
		if isUnsafeRune(r) || isJoiner(r) {
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
	runes := []rune(s)
	for i, r := range runes {
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
		case isJoiner(r) && !joinerAllowed(runes, i):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isJoiner reports the four invisible characters that ordinary text needs in
// one place only: the zero-width joiner and non-joiner, and variation
// selectors 15 and 16 (text and emoji presentation).
func isJoiner(r rune) bool {
	return r == 0x200C || r == 0x200D || r == 0xFE0E || r == 0xFE0F
}

// joinerAllowed reports whether the joiner at runes[i] sits where real text
// puts it: inside an emoji sequence (woman + joiner + laptop, heart +
// selector, a keycap) or between two letters of a script that joins.
// Anywhere else, and in any run of them, it carries nothing a reader can
// see, only what a program can decode, and is removed.
func joinerAllowed(runes []rune, i int) bool {
	prev, next := rune(0), rune(0)
	if i > 0 {
		prev = runes[i-1]
	}
	if i+1 < len(runes) {
		next = runes[i+1]
	}
	switch runes[i] {
	case 0xFE0E, 0xFE0F:
		if prev >= '0' && prev <= '9' || prev == '#' || prev == '*' {
			return next == 0x20E3 // keycap
		}
		return isEmojiBase(prev)
	case 0x200D:
		if prev == 0xFE0F && i > 1 {
			prev = runes[i-2]
		}
		return isEmojiBase(prev) && isEmojiBase(next) || isJoiningLetter(prev) && isJoiningLetter(next)
	default: // 0x200C
		return isJoiningLetter(prev) && isJoiningLetter(next)
	}
}

// isEmojiBase reports runes that emoji sequences are built from.
func isEmojiBase(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF, // pictographs, flags, skin tones
		r >= 0x2190 && r <= 0x21FF, // arrows
		r >= 0x2300 && r <= 0x23FF, // technical
		r >= 0x25A0 && r <= 0x27BF, // shapes, symbols, dingbats
		r >= 0x2900 && r <= 0x297F, r >= 0x2B00 && r <= 0x2BFF,
		r == 0x00A9, r == 0x00AE, r == 0x203C, r == 0x2049, r == 0x2122, r == 0x2139, r == 0x24C2,
		r == 0x3030, r == 0x303D, r == 0x3297, r == 0x3299:
		return true
	}
	return false
}

// joiningScripts are the scripts whose spelling uses the zero-width joiner
// and non-joiner between letters.
var joiningScripts = []*unicode.RangeTable{
	unicode.Arabic, unicode.Syriac, unicode.Mongolian, unicode.Devanagari, unicode.Bengali,
	unicode.Gurmukhi, unicode.Gujarati, unicode.Oriya, unicode.Tamil, unicode.Telugu,
	unicode.Kannada, unicode.Malayalam, unicode.Sinhala, unicode.Myanmar, unicode.Khmer, unicode.Tibetan,
}

func isJoiningLetter(r rune) bool {
	return r >= 0x80 && unicode.In(r, joiningScripts...)
}
