package email

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// Contact is a correspondent found in cached mail, for completing
// recipients. Name and Address come from message headers: third-party text,
// sanitized, but not verified to belong together.
type Contact struct {
	Name    string
	Address string // lower-cased
	// Sent counts messages the user wrote to the address; Received counts
	// messages from it.
	Sent     int
	Received int
	Last     time.Time // newest message involving the address
}

// Formatted returns the contact as a recipient entry.
func (c Contact) Formatted() string { return FormatAddress(c.Name, c.Address) }

// score ranks contacts: people the user writes to come first, then
// frequent senders; automated senders are pushed down.
func (c Contact) score() int {
	s := c.Sent*10 + min(c.Received, 20)
	if isAutomated(c.Address) {
		s -= 1000
	}
	return s
}

func isAutomated(addr string) bool {
	local, _, _ := strings.Cut(addr, "@")
	for _, marker := range []string{"noreply", "no-reply", "no_reply", "donotreply", "do-not-reply", "mailer-daemon", "bounce", "notification"} {
		if strings.Contains(local, marker) {
			return true
		}
	}
	return false
}

// ContactBook collects contacts while scanning messages.
type ContactBook struct {
	byAddr map[string]*Contact
	own    AddressSet
}

// NewContactBook returns a book that ignores the user's own addresses.
func NewContactBook(own AddressSet) *ContactBook {
	return &ContactBook{byAddr: map[string]*Contact{}, own: own}
}

// Add records one address entry ("Name <addr>") seen on a message. sent
// says the user wrote the message (the entry is a recipient); otherwise the
// entry is the sender.
func (b *ContactBook) Add(entry string, sent bool, when time.Time) {
	addr, ok := SingleAddress(entry)
	if !ok || b.own[addr] {
		return
	}
	c := b.byAddr[addr]
	if c == nil {
		c = &Contact{Address: addr}
		b.byAddr[addr] = c
	}
	if sent {
		c.Sent++
	} else {
		c.Received++
	}
	// Keep the name from the newest message that has one; a name the user
	// typed themselves (sent mail) is not overwritten by a sender's.
	if name := displayName(entry, addr); name != "" && (c.Name == "" || when.After(c.Last)) {
		c.Name = name
	}
	if when.After(c.Last) {
		c.Last = when
	}
}

// Contacts returns the collected contacts, best first.
func (b *ContactBook) Contacts() []Contact {
	out := make([]Contact, 0, len(b.byAddr))
	for _, c := range b.byAddr {
		out = append(out, *c)
	}
	sortContacts(out)
	return out
}

func sortContacts(list []Contact) {
	sort.SliceStable(list, func(i, j int) bool {
		if si, sj := list[i].score(), list[j].score(); si != sj {
			return si > sj
		}
		if !list[i].Last.Equal(list[j].Last) {
			return list[i].Last.After(list[j].Last)
		}
		return list[i].Address < list[j].Address
	})
}

// displayName extracts the display name of a formatted entry.
func displayName(entry, addr string) string {
	entry = strings.TrimSpace(SanitizeLine(entry))
	i := strings.LastIndex(entry, "<")
	if i <= 0 {
		return ""
	}
	name := strings.TrimSpace(entry[:i])
	if len(name) >= 2 && strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) {
		name = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(name[1 : len(name)-1])
	}
	if strings.EqualFold(name, addr) {
		return ""
	}
	return name
}

// MatchContacts returns up to limit contacts matching what the user has
// typed, from an already ranked list. A match at the start of the name, of a
// word in the name, or of the address ranks above a match elsewhere.
// Matching ignores case and diacritics, so "zofie" finds "Žofie".
func MatchContacts(contacts []Contact, query string, limit int) []Contact {
	q := fold(strings.TrimSpace(query))
	if q == "" || limit <= 0 {
		return nil
	}
	var strong, weak []Contact
	for _, c := range contacts {
		name, addr := fold(c.Name), fold(c.Address)
		switch {
		case strings.HasPrefix(addr, q), strings.HasPrefix(name, q), wordPrefix(name, q):
			strong = append(strong, c)
		case strings.Contains(addr, q), strings.Contains(name, q):
			weak = append(weak, c)
		}
		if len(strong) >= limit {
			break
		}
	}
	out := append(strong, weak...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func wordPrefix(name, q string) bool {
	for _, word := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.HasPrefix(word, q) {
			return true
		}
	}
	return false
}

// fold lower-cases and strips the diacritics common in European names.
func fold(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if repl, ok := foldTable[r]; ok {
			b.WriteString(repl)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var foldTable = func() map[rune]string {
	t := map[rune]string{}
	for repl, chars := range map[string]string{
		"a": "áàâäãåāăą", "c": "çćčĉċ", "d": "ďđ", "e": "éèêëēěęė", "g": "ğĝġģ", "i": "íìîïīįı",
		"l": "ľĺłļ", "n": "ñńňņ", "o": "óòôöõōőø", "r": "řŕŗ", "s": "śšşŝș", "t": "ťţț",
		"u": "úùûüūůűų", "y": "ýÿŷ", "z": "źžż", "ss": "ß", "ae": "æ", "oe": "œ",
	} {
		for _, r := range chars {
			t[r] = repl
		}
	}
	return t
}()
