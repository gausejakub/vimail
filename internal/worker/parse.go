package worker

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message/mail"
	"github.com/gausejakub/vimail/internal/email"
)

// ParseEnvelope converts an IMAP envelope into an email.Message.
func ParseEnvelope(uid uint32, env *imap.Envelope, flags []imap.Flag) email.Message {
	msg := email.Message{
		UID: uid,
		ID:  fmt.Sprintf("%d", uid),
	}

	if env != nil {
		msg.Subject = env.Subject
		msg.Date = env.Date

		if len(env.From) > 0 {
			msg.From = formatAddress(env.From[0])
		}
		if len(env.To) > 0 {
			msg.To = formatAddress(env.To[0])
		}
	}

	// Default unread = true, mark as read if \Seen present.
	msg.Unread = true
	for _, f := range flags {
		switch f {
		case imap.FlagSeen:
			msg.Unread = false
		case imap.FlagFlagged:
			msg.Flagged = true
		}
	}

	return msg
}

func formatAddress(addr imap.Address) string {
	name := addr.Name
	emailAddr := addr.Addr()

	if name != "" {
		return name
	}
	// No display name — use the local part of the email.
	if at := strings.Index(emailAddr, "@"); at > 0 {
		return emailAddr[:at]
	}
	return emailAddr
}

// BodyResult holds both the plain-text and raw HTML versions of an email body.
type BodyResult struct {
	Text string
	HTML string
}

// ParseBody extracts text and HTML content from a message.
// Returns a BodyResult with a displayable Text and the raw HTML (for "open in browser").
func ParseBody(data []byte) (BodyResult, error) {
	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		// Not valid MIME — treat as raw text.
		raw := string(data)
		if looksLikeHTML(raw) {
			return BodyResult{Text: stripHTML(raw), HTML: raw}, nil
		}
		return BodyResult{Text: raw}, nil
	}

	var textBody string
	var htmlBody string
	collectParts(mr, &textBody, &htmlBody)

	// Build display text: prefer text/plain if it's meaningful.
	var display string
	switch {
	case textBody != "" && !looksLikeHTML(textBody) && len(textBody) > 20:
		display = textBody
	case htmlBody != "":
		display = stripHTML(htmlBody)
	case textBody != "":
		display = stripHTML(textBody)
	default:
		display = "(no text content)"
	}

	return BodyResult{Text: display, HTML: htmlBody}, nil
}

// collectParts recursively walks MIME parts to find text/plain and text/html.
func collectParts(mr *mail.Reader, text, html *string) {
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}

		ct := p.Header.Get("Content-Type")

		// Recurse into nested multipart (multipart/alternative, multipart/mixed, etc.).
		if strings.HasPrefix(ct, "multipart/") {
			nested, err := mail.CreateReader(p.Body)
			if err == nil {
				collectParts(nested, text, html)
			}
			continue
		}

		partData, err := io.ReadAll(p.Body)
		if err != nil {
			continue
		}

		switch {
		case strings.HasPrefix(ct, "text/plain") || ct == "":
			if *text == "" {
				*text = string(partData)
			}
		case strings.HasPrefix(ct, "text/html"):
			if *html == "" {
				*html = string(partData)
			}
		}
	}
}

func looksLikeHTML(s string) bool {
	return strings.Contains(s, "<html") || strings.Contains(s, "<HTML") ||
		strings.Contains(s, "<body") || strings.Contains(s, "<div") ||
		strings.Contains(s, "<table") || strings.Contains(s, "<p>")
}

var (
	reStyle   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reScript  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reTag     = regexp.MustCompile(`<[^>]+>`)
	reSpaces = regexp.MustCompile(`[ \t]+`)
	reBlanks = regexp.MustCompile(`\n{3,}`)
)

// stripHTML converts HTML to readable plain text.
func stripHTML(raw string) string {
	// Remove style and script blocks.
	s := reStyle.ReplaceAllString(raw, "")
	s = reScript.ReplaceAllString(s, "")

	// Insert newlines for block elements.
	for _, tag := range []string{"br", "BR", "p", "P", "div", "DIV", "tr", "TR", "li", "LI", "h1", "h2", "h3", "h4", "h5", "h6"} {
		s = strings.ReplaceAll(s, "<"+tag+">", "\n")
		s = strings.ReplaceAll(s, "<"+tag+" ", "\n<"+tag+" ")
		s = strings.ReplaceAll(s, "</"+tag+">", "\n")
	}

	// Strip all remaining tags.
	s = reTag.ReplaceAllString(s, "")

	// Decode all HTML entities (&nbsp;, &zwnj;, &#8204;, etc.).
	s = html.UnescapeString(s)

	// Normalize whitespace.
	s = reSpaces.ReplaceAllString(s, " ")
	s = reBlanks.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}

// FolderName maps an IMAP mailbox name to a display name.
func FolderName(mailboxName string) string {
	lower := strings.ToLower(mailboxName)

	// Normalize hierarchical names (e.g. "INBOX.Sent" → "sent", "INBOX/Trash" → "trash").
	base := lower
	if i := strings.LastIndexAny(lower, "./"); i >= 0 {
		base = lower[i+1:]
	}

	switch {
	case lower == "inbox":
		return "Inbox"
	// Sent
	case lower == "[gmail]/sent mail" || base == "sent" || lower == "sent items" || lower == "sent messages" || base == "odeslane" || base == "odeslaná pošta":
		return "Sent"
	// Drafts
	case lower == "[gmail]/drafts" || base == "drafts" || base == "koncepty" || base == "rozepsane":
		return "Drafts"
	// Trash
	case lower == "[gmail]/trash" || base == "trash" || lower == "deleted items" || lower == "deleted messages" || base == "koš" || base == "kos":
		return "Trash"
	// Spam
	case lower == "[gmail]/spam" || base == "junk" || lower == "junk email" || base == "spam":
		return "Spam"
	// Archive
	case base == "archive" || base == "archiv":
		return "Archive"
	// Gmail-specific
	case lower == "[gmail]/all mail":
		return "All Mail"
	case lower == "[gmail]/starred":
		return "Starred"
	case lower == "[gmail]/important":
		return "Important"
	default:
		return mailboxName
	}
}
