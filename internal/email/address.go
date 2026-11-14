package email

import (
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-message/charset"
)

// FormatAddress renders one mailbox as "Name <addr>", quoting the display
// name when it contains RFC 5322 specials so the result parses back
// unchanged (a name such as "Doe, John" must not split into two addresses).
//
// The address itself is made safe too: a local part that needs quoting is
// quoted again and the domain loses characters it can never contain.
// net/mail hands back `"a>, <b"@x` unquoted as `a>, <b@x`; written out as
// is, that one mailbox would read as two (`<a>, <b@x>`), and a check that
// looked at one of them would pass for a header that carries both.
func FormatAddress(name, addr string) string {
	name, addr = strings.TrimSpace(SanitizeLine(name)), strings.TrimSpace(SanitizeLine(addr))
	bare := addr
	addr = quoteAddrSpec(addr)
	if name == "" || strings.EqualFold(name, bare) {
		return addr
	}
	if addr == "" {
		return name
	}
	if strings.ContainsAny(name, `()<>[]:;@\,."`) {
		name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
	return name + " <" + addr + ">"
}

// quoteAddrSpec returns local@domain with the local part quoted when RFC 5322
// requires it, so the result always parses back as exactly one mailbox.
func quoteAddrSpec(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr
	}
	local, domain := addr[:at], addr[at+1:]
	domain = strings.Map(func(r rune) rune {
		if strings.ContainsRune("<>,;:\"()\\@ \t", r) {
			return -1
		}
		return r
	}, domain)
	if localNeedsQuoting(local) {
		local = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(local) + `"`
	}
	return local + "@" + domain
}

func localNeedsQuoting(local string) bool {
	if local == "" || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return true
	}
	for _, r := range local {
		switch {
		case r >= 0x80, r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(".!#$%&'*+-/=?^_`{|}~", r):
		default:
			return true
		}
	}
	return false
}

// SingleAddress returns the lower-cased address of entry only when entry is
// exactly one well-formed mailbox. Checks that decide what a message may
// carry (the From identity) use it instead of AddrSpec, whose lenient
// fallback is meant for display and de-duplication.
func SingleAddress(entry string) (string, bool) {
	a, err := mail.ParseAddress(strings.TrimSpace(SanitizeLine(entry)))
	if err != nil || a.Address == "" {
		return "", false
	}
	return strings.ToLower(a.Address), true
}

// JoinAddresses joins formatted addresses for display and search.
func JoinAddresses(list []string) string { return strings.Join(list, ", ") }

// AddrSpec returns the lower-cased bare address of one mailbox entry. The
// display name is attacker-controlled and may contain "<victim@corp>", so a
// value that does not parse uses the LAST angle-bracket group.
func AddrSpec(entry string) string {
	entry = SanitizeLine(entry)
	if a, err := mail.ParseAddress(entry); err == nil {
		return strings.ToLower(a.Address)
	}
	if end := strings.LastIndex(entry, ">"); end > 0 {
		if start := strings.LastIndex(entry[:end], "<"); start >= 0 {
			return strings.ToLower(strings.TrimSpace(entry[start+1 : end]))
		}
	}
	return strings.ToLower(strings.TrimSpace(entry))
}

// NormalizeAddresses parses user-supplied recipients. Each entry may itself
// hold several comma-separated addresses. The result has one formatted
// mailbox per element, duplicates removed; an unparseable entry is an error
// naming it, so a typo is never silently dropped.
func NormalizeAddresses(entries []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, entry := range entries {
		entry = strings.TrimSpace(SanitizeLine(entry))
		if entry == "" {
			continue
		}
		list, err := mail.ParseAddressList(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %v", entry, err)
		}
		for _, a := range list {
			key := strings.ToLower(a.Address)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, FormatAddress(a.Name, a.Address))
		}
	}
	return out, nil
}

// SplitAddressField parses a free-text address field such as the composer's
// To line ("a@x, Bob <b@y>").
func SplitAddressField(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	return NormalizeAddresses([]string{s})
}

// ReplySubject prefixes "Re: " unless the subject already is a reply.
func ReplySubject(subject string) string {
	subject = strings.TrimSpace(SanitizeLine(subject))
	if hasPrefixFold(subject, "re:") {
		return subject
	}
	return "Re: " + subject
}

// ForwardSubject prefixes "Fwd: " unless the subject already is a forward.
func ForwardSubject(subject string) string {
	subject = strings.TrimSpace(SanitizeLine(subject))
	if hasPrefixFold(subject, "fwd:") || hasPrefixFold(subject, "fw:") {
		return subject
	}
	return "Fwd: " + subject
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// maxReferences bounds the References header of a reply. RFC 5322 allows
// trimming; the first (root) and the most recent ids are what clients thread on.
const maxReferences = 20

// ReplyReferences builds the References list of a reply to orig: the
// original's References (or In-Reply-To when it has none) plus its own
// Message-ID.
func ReplyReferences(orig Message) []string {
	refs := append([]string(nil), orig.References...)
	if len(refs) == 0 && orig.InReplyTo != "" {
		refs = []string{orig.InReplyTo}
	}
	if orig.MessageID != "" {
		refs = append(refs, orig.MessageID)
	}
	refs = dedupe(refs)
	if len(refs) > maxReferences {
		refs = append(refs[:1], refs[len(refs)-(maxReferences-1):]...)
	}
	return refs
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	out := list[:0:0]
	for _, v := range list {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// ThreadIDFor derives a conversation id: the root of References, else the
// parent from In-Reply-To, else the message's own Message-ID. Messages of one
// conversation share it as long as their clients keep References intact.
func ThreadIDFor(messageID, inReplyTo string, references []string) string {
	for _, r := range references {
		if r != "" {
			return r
		}
	}
	if inReplyTo != "" {
		return inReplyTo
	}
	return messageID
}

// AddressSet answers whether an address belongs to the user.
type AddressSet map[string]bool

// NewAddressSet builds a set from address entries ("Name <a@b>" or "a@b").
func NewAddressSet(entries ...string) AddressSet {
	s := AddressSet{}
	for _, e := range entries {
		if a := AddrSpec(e); a != "" {
			s[a] = true
		}
	}
	return s
}

// Has reports whether entry's address is in the set.
func (s AddressSet) Has(entry string) bool { return s[AddrSpec(entry)] }

// ReplyRecipients computes the recipients of a reply to orig. To is the
// original's Reply-To, or its From. When the original was written by the
// user (a message in Sent), the reply goes to its original recipients
// instead. With all=true, the original's To and Cc are added to Cc. The
// user's own addresses are never included, and nobody appears twice.
func ReplyRecipients(orig Message, all bool, own AddressSet) (to, cc []string) {
	seen := map[string]bool{}
	add := func(dst *[]string, entries []string) {
		for _, e := range entries {
			a := AddrSpec(e)
			if a == "" || own[a] || seen[a] {
				continue
			}
			seen[a] = true
			*dst = append(*dst, e)
		}
	}
	primary := orig.ReplyTo
	if len(primary) == 0 && orig.From != "" {
		primary = []string{orig.From}
	}
	fromSelf := orig.From != "" && own.Has(orig.From) && len(orig.ReplyTo) == 0
	if fromSelf {
		add(&to, orig.ToList)
	} else {
		add(&to, primary)
	}
	if all {
		if !fromSelf {
			add(&cc, orig.ToList)
		}
		add(&cc, orig.Cc)
	}
	return to, cc
}

// PickIdentity chooses the address a reply to orig should come from: the
// first of the account's identities that the original was addressed to (To,
// then Cc, then Delivered-To/X-Original-To). It returns "" when none match,
// meaning the account's primary address.
func PickIdentity(orig Message, identities []string) string {
	ids := map[string]string{}
	for _, id := range identities {
		if a, ok := SingleAddress(id); ok { // a malformed identity is never picked
			if _, dup := ids[a]; !dup {
				ids[a] = id
			}
		}
	}
	for _, list := range [][]string{orig.ToList, orig.Cc, orig.DeliveredTo} {
		for _, e := range list {
			if id, ok := ids[AddrSpec(e)]; ok {
				return id
			}
		}
	}
	return ""
}

// QuoteReply returns the attribution line and the original body quoted with
// "> ", for placing under the user's reply text.
func QuoteReply(orig Message) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("On %s, %s wrote:\n", orig.Date.Format("Mon, 2 Jan 2006 at 15:04"), SanitizeLine(orig.From)))
	body := strings.TrimRight(SanitizeText(orig.Body), "\n")
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, ">") {
			b.WriteString(">" + line + "\n")
		} else {
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

// ForwardBody returns the inline header block and body of a forwarded message.
func ForwardBody(orig Message) string {
	var b strings.Builder
	b.WriteString("---------- Forwarded message ----------\n")
	b.WriteString("From: " + SanitizeLine(orig.From) + "\n")
	b.WriteString("Date: " + orig.Date.Format(time.RFC1123Z) + "\n")
	b.WriteString("Subject: " + SanitizeLine(orig.Subject) + "\n")
	if len(orig.ToList) > 0 {
		b.WriteString("To: " + JoinAddresses(orig.ToList) + "\n")
	} else if orig.To != "" {
		b.WriteString("To: " + SanitizeLine(orig.To) + "\n")
	}
	if len(orig.Cc) > 0 {
		b.WriteString("Cc: " + JoinAddresses(orig.Cc) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(SanitizeText(orig.Body))
	return b.String()
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charset.Reader}

// DecodeHeaderWords decodes RFC 2047 encoded-words ("=?UTF-8?Q?...?=").
// Some mailers put them inside MIME parameters such as an attachment's
// filename, where standard parameter parsing leaves them encoded. Values
// without encoded words, or that fail to decode, are returned unchanged.
func DecodeHeaderWords(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	if dec, err := wordDecoder.DecodeHeader(s); err == nil {
		return dec
	}
	return s
}
