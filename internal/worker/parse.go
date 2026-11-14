package worker

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/jaytaylor/html2text"
)

// ParseEnvelope converts an IMAP envelope into an email.Message.
func ParseEnvelope(uid uint32, env *imap.Envelope, flags []imap.Flag) email.Message {
	msg := email.Message{
		UID: uid,
		ID:  fmt.Sprintf("%d", uid),
	}

	if env != nil {
		msg.Subject = email.SanitizeLine(env.Subject)
		msg.Date = env.Date
		msg.MessageID = env.MessageID

		if len(env.From) > 0 {
			msg.From = formatAddress(env.From[0])
		}
		msg.ToList = formatAddresses(env.To)
		msg.To = email.JoinAddresses(msg.ToList)
		msg.Cc = formatAddresses(env.Cc)
		msg.Bcc = formatAddresses(env.Bcc)
		msg.ReplyTo = formatAddresses(env.ReplyTo)
		// Servers fill Reply-To with From when the header is absent
		// (RFC 3501); keep it only when it says something different.
		if len(msg.ReplyTo) == 1 && msg.From != "" && email.AddrSpec(msg.ReplyTo[0]) == email.AddrSpec(msg.From) {
			msg.ReplyTo = nil
		}
		if len(env.InReplyTo) > 0 {
			msg.InReplyTo = env.InReplyTo[0]
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
		case imap.FlagAnswered:
			msg.Answered = true
		}
	}

	return msg
}

// extraHeaderSection fetches the headers the envelope lacks: References for
// threading, and Delivered-To / X-Original-To for choosing the identity a
// reply is sent from.
var extraHeaderSection = &imap.FetchItemBodySection{
	Specifier:    imap.PartSpecifierHeader,
	HeaderFields: []string{"References", "Delivered-To", "X-Original-To"},
	Peek:         true,
}

// headerFetchOptions are the FETCH items that fill an email.Message header.
func headerFetchOptions() *imap.FetchOptions {
	return &imap.FetchOptions{
		Envelope:    true,
		Flags:       true,
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{extraHeaderSection},
	}
}

// parseFetchedHeaders builds a message from a header FETCH response.
func parseFetchedHeaders(buf *imapclient.FetchMessageBuffer) email.Message {
	msg := ParseEnvelope(uint32(buf.UID), buf.Envelope, buf.Flags)
	applyExtraHeaders(&msg, buf.FindBodySection(extraHeaderSection))
	return msg
}

// applyExtraHeaders parses References, Delivered-To and X-Original-To.
func applyExtraHeaders(msg *email.Message, raw []byte) {
	if len(raw) == 0 {
		return
	}
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(append(bytes.TrimRight(raw, "\r\n"), "\r\n\r\n"...))))
	if err != nil {
		return
	}
	mh := mail.Header{Header: message.Header{Header: h}}
	if refs, err := mh.MsgIDList("References"); err == nil {
		msg.References = refs
	}
	seen := map[string]bool{}
	for _, key := range []string{"Delivered-To", "X-Original-To"} {
		for _, v := range h.Values(key) {
			if addrs, err := mail.ParseAddressList(v); err == nil {
				for _, a := range addrs {
					addr := email.SanitizeLine(strings.ToLower(a.Address))
					if addr != "" && !seen[addr] {
						seen[addr] = true
						msg.DeliveredTo = append(msg.DeliveredTo, addr)
					}
				}
			}
		}
	}
}

func formatAddresses(list []imap.Address) []string {
	var out []string
	for _, a := range list {
		if a.IsGroupStart() || a.IsGroupEnd() {
			continue
		}
		if s := formatAddress(a); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func formatAddress(addr imap.Address) string {
	// Display names and addresses are attacker-controlled header text.
	return email.FormatAddress(addr.Name, addr.Addr())
}

// BodyResult holds both the plain-text and raw HTML versions of an email body,
// plus any attachment metadata discovered during parsing.
type BodyResult struct {
	Text string
	// Plain is the text/plain part as written ("" when there is none); Text
	// prefers a conversion of the HTML part.
	Plain       string
	HTML        string
	Attachments []email.Attachment
}

// ParseBody extracts text and HTML content from a message.
// Returns a BodyResult with a displayable Text, the raw HTML (for "open in browser"),
// and metadata for any attachments found.
func ParseBody(data []byte) (BodyResult, error) {
	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		// Not valid MIME — treat as raw text.
		raw := string(data)
		if looksLikeHTML(raw) {
			return BodyResult{Text: email.SanitizeText(stripHTML(raw)), HTML: raw}, nil
		}
		return BodyResult{Text: email.SanitizeText(raw)}, nil
	}

	var textBody string
	var htmlBody string
	var attachments []email.Attachment
	collectParts(mr, &textBody, &htmlBody, &attachments, "")

	// Build display text: prefer HTML conversion (cleaner output) when available,
	// fall back to text/plain only when there is no HTML part.
	var display string
	switch {
	case htmlBody != "":
		display = stripHTML(htmlBody)
	case textBody != "" && !looksLikeHTML(textBody):
		display = textBody
	case textBody != "":
		display = stripHTML(textBody)
	default:
		display = "(no text content)"
	}

	// An invitation is shown as a block above the text: what, when, who.
	if block := eventBlock(data); block != "" {
		display = block + "\n" + display
	}

	// Sanitize once at ingest so the cache, preview, reply quoting, export
	// and MCP all receive text that is safe to print to a terminal.
	return BodyResult{Text: email.SanitizeText(display), HTML: htmlBody, Plain: email.SanitizeText(textBody), Attachments: attachments}, nil
}

const (
	maxPartSize  = 25 * 1024 * 1024 // 25 MB per MIME part
	maxMIMEDepth = 20               // maximum nesting depth for multipart
)

// collectParts recursively walks MIME parts to find text/plain, text/html, and attachments.
// partPrefix tracks the MIME part numbering (e.g. "1", "1.2") for IMAP BODY[part] fetching.
func collectParts(mr *mail.Reader, text, html *string, attachments *[]email.Attachment, partPrefix string) {
	collectPartsDepth(mr, text, html, attachments, partPrefix, 0)
}

func collectPartsDepth(mr *mail.Reader, text, html *string, attachments *[]email.Attachment, partPrefix string, depth int) {
	partIdx := 0
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		partIdx++
		partNum := fmt.Sprintf("%d", partIdx)
		if partPrefix != "" {
			partNum = partPrefix + "." + partNum
		}

		ct := p.Header.Get("Content-Type")
		disp := p.Header.Get("Content-Disposition")

		// Recurse into nested multipart (multipart/alternative, multipart/mixed, etc.).
		if strings.HasPrefix(ct, "multipart/") {
			if depth >= maxMIMEDepth {
				continue
			}
			nested, err := mail.CreateReader(p.Body)
			if err == nil {
				collectPartsDepth(nested, text, html, attachments, partNum, depth+1)
			}
			continue
		}

		// Check if this part is an attachment.
		isAttachment := strings.HasPrefix(disp, "attachment")
		filename := extractFilename(disp, ct)
		// Inline parts with a filename (e.g. inline images) are also attachments.
		if filename != "" && !strings.HasPrefix(ct, "text/") {
			isAttachment = true
		}

		if isAttachment {
			// Read to measure size, but don't store the data.
			partData, err := io.ReadAll(io.LimitReader(p.Body, maxPartSize))
			size := 0
			if err == nil {
				size = len(partData)
			}
			contentType := ct
			if idx := strings.Index(contentType, ";"); idx > 0 {
				contentType = strings.TrimSpace(contentType[:idx])
			}
			if filename == "" {
				filename = fmt.Sprintf("attachment-%s", partNum)
			}
			*attachments = append(*attachments, email.Attachment{
				Filename:    filename,
				ContentType: contentType,
				Size:        size,
				PartNum:     partNum,
			})
			continue
		}

		partData, err := io.ReadAll(io.LimitReader(p.Body, maxPartSize))
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

// extractFilename pulls the filename from Content-Disposition or Content-Type
// headers. RFC 2231 parameters (filename*=UTF-8”...) are decoded by the MIME
// parser; RFC 2047 encoded-words, which many mailers put inside the quoted
// value ("=?UTF-8?Q?Digifest_2026...?="), are decoded afterwards.
func extractFilename(disp, ct string) string {
	clean := func(v string) string {
		return strings.TrimSpace(email.SanitizeLine(email.DecodeHeaderWords(strings.Trim(v, `"' `))))
	}
	for _, h := range []struct{ value, param string }{{disp, "filename"}, {ct, "name"}} {
		if _, params, err := mime.ParseMediaType(h.value); err == nil {
			if v := clean(params[h.param]); v != "" {
				return v
			}
		}
	}
	// Malformed headers: fall back to a lenient scan of the parameters.
	for _, header := range []string{disp, ct} {
		for _, param := range strings.Split(header, ";") {
			param = strings.TrimSpace(param)
			lower := strings.ToLower(param)
			if strings.HasPrefix(lower, "filename=") || strings.HasPrefix(lower, "name=") {
				if val := clean(param[strings.Index(param, "=")+1:]); val != "" {
					return val
				}
			}
		}
	}
	return ""
}

// AttachmentData holds the raw bytes of an extracted attachment.
type AttachmentData struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ExtractAttachmentData parses a raw RFC 5322 message and returns the binary data
// for all attachment parts.
func ExtractAttachmentData(data []byte) ([]AttachmentData, error) {
	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	var result []AttachmentData
	extractAttachmentParts(mr, &result)
	return result, nil
}

func extractAttachmentParts(mr *mail.Reader, result *[]AttachmentData) {
	extractAttachmentPartsDepth(mr, result, 0)
}

func extractAttachmentPartsDepth(mr *mail.Reader, result *[]AttachmentData, depth int) {
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}

		ct := p.Header.Get("Content-Type")
		disp := p.Header.Get("Content-Disposition")

		if strings.HasPrefix(ct, "multipart/") {
			if depth >= maxMIMEDepth {
				continue
			}
			nested, err := mail.CreateReader(p.Body)
			if err == nil {
				extractAttachmentPartsDepth(nested, result, depth+1)
			}
			continue
		}

		isAttachment := strings.HasPrefix(disp, "attachment")
		filename := extractFilename(disp, ct)
		if filename != "" && !strings.HasPrefix(ct, "text/") {
			isAttachment = true
		}

		if isAttachment {
			partData, err := io.ReadAll(io.LimitReader(p.Body, maxPartSize))
			if err != nil {
				continue
			}
			if filename == "" {
				filename = "attachment"
			}
			*result = append(*result, AttachmentData{
				Filename:    filename,
				ContentType: mediaType(ct),
				Data:        partData,
			})
		}
	}
}

func looksLikeHTML(s string) bool {
	return strings.Contains(s, "<html") || strings.Contains(s, "<HTML") ||
		strings.Contains(s, "<body") || strings.Contains(s, "<div") ||
		strings.Contains(s, "<table") || strings.Contains(s, "<p>")
}

// stripHTML converts HTML to readable plain text using html2text.
func stripHTML(raw string) string {
	text, err := html2text.FromString(raw, html2text.Options{
		PrettyTables: false,
		OmitLinks:    true,
	})
	if err != nil {
		return raw
	}
	return text
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
	case lower == "[gmail]/sent mail" || lower == "[gmail]/odeslaná pošta" ||
		base == "sent" || lower == "sent items" || lower == "sent messages" ||
		base == "odeslane" || base == "odeslaná pošta":
		return "Sent"
	// Drafts
	case lower == "[gmail]/drafts" || lower == "[gmail]/koncepty" ||
		base == "drafts" || base == "koncepty" || base == "rozepsane":
		return "Drafts"
	// Trash
	case lower == "[gmail]/trash" || lower == "[gmail]/bin" || lower == "[gmail]/koš" ||
		base == "trash" || base == "bin" || base == "koš" || base == "kos" ||
		lower == "deleted items" || lower == "deleted messages":
		return "Trash"
	// Spam
	case lower == "[gmail]/spam" || lower == "[gmail]/nevyžádaná pošta" ||
		base == "junk" || lower == "junk email" || base == "spam":
		return "Spam"
	// Archive
	case base == "archive" || base == "archiv":
		return "Archive"
	// Gmail-specific (with or without / separator)
	case lower == "[gmail]/all mail" || lower == "[gmail]all mail" ||
		lower == "[gmail]/všechny zprávy" || lower == "[gmail]všechny zprávy" ||
		lower == "[gmail]/všechna pošta" || lower == "[gmail]všechna pošta":
		return "All Mail"
	case lower == "[gmail]/starred" || lower == "[gmail]starred" ||
		lower == "[gmail]/označené hvězdičkou" || lower == "[gmail]označené hvězdičkou":
		return "Starred"
	case lower == "[gmail]/important" || lower == "[gmail]important" ||
		lower == "[gmail]/důležité" || lower == "[gmail]důležité":
		return "Important"
	default:
		return mailboxName
	}
}

// mediaType returns the type/subtype of a Content-Type value. The header is
// third-party text, so a value that is not a plain type/subtype (it may hide
// invisible characters, or claim to be multipart) becomes
// application/octet-stream.
func mediaType(ct string) string {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = ct
		if i := strings.Index(mt, ";"); i >= 0 {
			mt = mt[:i]
		}
		mt = strings.TrimSpace(strings.ToLower(mt))
	}
	typ, sub, ok := strings.Cut(mt, "/")
	if !ok || !isMIMEToken(typ) || !isMIMEToken(sub) || typ == "multipart" {
		return "application/octet-stream"
	}
	return mt
}

func isMIMEToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$&^_.+-", r)) {
			return false
		}
	}
	return true
}
