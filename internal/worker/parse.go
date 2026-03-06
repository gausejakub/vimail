package worker

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message/mail"
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

	if name != "" && emailAddr != "" {
		return fmt.Sprintf("%s <%s>", name, emailAddr)
	}
	if emailAddr != "" {
		return emailAddr
	}
	return name
}

// BodyResult holds both the plain-text and raw HTML versions of an email body,
// plus any attachment metadata discovered during parsing.
type BodyResult struct {
	Text        string
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
			return BodyResult{Text: stripHTML(raw), HTML: raw}, nil
		}
		return BodyResult{Text: raw}, nil
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

	return BodyResult{Text: display, HTML: htmlBody, Attachments: attachments}, nil
}

// collectParts recursively walks MIME parts to find text/plain, text/html, and attachments.
// partPrefix tracks the MIME part numbering (e.g. "1", "1.2") for IMAP BODY[part] fetching.
func collectParts(mr *mail.Reader, text, html *string, attachments *[]email.Attachment, partPrefix string) {
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
			nested, err := mail.CreateReader(p.Body)
			if err == nil {
				collectParts(nested, text, html, attachments, partNum)
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
			partData, err := io.ReadAll(p.Body)
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

// extractFilename pulls the filename from Content-Disposition or Content-Type headers.
func extractFilename(disp, ct string) string {
	for _, header := range []string{disp, ct} {
		for _, param := range strings.Split(header, ";") {
			param = strings.TrimSpace(param)
			lower := strings.ToLower(param)
			if strings.HasPrefix(lower, "filename=") || strings.HasPrefix(lower, "name=") {
				val := param[strings.Index(param, "=")+1:]
				val = strings.Trim(val, `"' `)
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}

// AttachmentData holds the raw bytes of an extracted attachment.
type AttachmentData struct {
	Filename string
	Data     []byte
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
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}

		ct := p.Header.Get("Content-Type")
		disp := p.Header.Get("Content-Disposition")

		if strings.HasPrefix(ct, "multipart/") {
			nested, err := mail.CreateReader(p.Body)
			if err == nil {
				extractAttachmentParts(nested, result)
			}
			continue
		}

		isAttachment := strings.HasPrefix(disp, "attachment")
		filename := extractFilename(disp, ct)
		if filename != "" && !strings.HasPrefix(ct, "text/") {
			isAttachment = true
		}

		if isAttachment {
			partData, err := io.ReadAll(p.Body)
			if err != nil {
				continue
			}
			if filename == "" {
				filename = "attachment"
			}
			*result = append(*result, AttachmentData{
				Filename: filename,
				Data:     partData,
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
