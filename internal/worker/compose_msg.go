package worker

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/pgp"
)

// ComposeOptions controls how a message is serialized.
type ComposeOptions struct {
	// IncludeBcc writes the Bcc header. Only for copies the user keeps
	// (Sent, Drafts): the transmitted message must never carry it, or every
	// recipient learns who was blind-copied.
	IncludeBcc bool
	// MessageID and Date, when set, are used instead of fresh values so a
	// retried send and its Sent copy carry the same identity.
	MessageID string
	Date      time.Time
	// PGP signs and encrypts messages that ask for it; nil when PGP is off.
	PGP *pgp.Client
	// DraftCopy encrypts to the sender alone (the copy in the Drafts folder).
	DraftCopy bool
	// memo lets the transmitted message and the kept copy share one
	// signing/encryption run.
	memo *pgpMemo
	// Extra headers (name → value), e.g. X-Vimail-Source on drafts.
	Extra map[string]string
}

// draftSourceHeader carries a draft's reply/forward source through the
// server Drafts folder, so a draft opened on another vimail install still
// flags the original when sent.
const draftSourceHeader = "X-Vimail-Source"

// draftMarkdownHeader marks a draft whose body is Markdown, so it is still
// sent with an HTML alternative after a round trip through the server.
const draftMarkdownHeader = "X-Vimail-Markdown"

// ComposeMessage serializes req as an RFC 5322 message: text/plain alone, or
// multipart/mixed with attachments. Recipients must already be validated
// (email.NormalizeAddresses); header values are sanitized again here because
// they may come from queued payloads or MCP arguments, and a CR/LF would let
// the caller inject headers.
func ComposeMessage(req SendRequest, opt ComposeOptions) (string, []byte, error) {
	from := headerValue(req.From)
	if from == "" {
		return "", nil, fmt.Errorf("no From address")
	}
	// Last line of defense for the identity checks in the MCP server and
	// the composer: a From that reads as several mailboxes is never written.
	if _, err := netmail.ParseAddress(from); err != nil {
		return "", nil, fmt.Errorf("From must be exactly one address: %v", err)
	}
	msgID := strings.Trim(headerValue(opt.MessageID), "<>")
	if msgID == "" {
		msgID = NewMessageID(from)
	}
	date := opt.Date
	if date.IsZero() {
		date = time.Now()
	}

	var h mail.Header
	h.SetDate(date)
	setAddressHeader(&h, "From", []string{from})
	setAddressHeader(&h, "To", req.allTo())
	if len(req.Cc) > 0 {
		setAddressHeader(&h, "Cc", req.Cc)
	}
	if opt.IncludeBcc && len(req.Bcc) > 0 {
		setAddressHeader(&h, "Bcc", req.Bcc)
	}
	h.SetSubject(headerValue(req.Subject))
	h.SetMessageID(msgID)
	if id := cleanMsgID(req.InReplyTo); id != "" {
		h.SetMsgIDList("In-Reply-To", []string{id})
	}
	if refs := cleanMsgIDs(req.References); len(refs) > 0 {
		h.SetMsgIDList("References", refs)
	}
	for k, v := range opt.Extra {
		if v = headerValue(v); v != "" {
			h.Set(headerValue(k), v)
		}
	}

	// PGP: build the content on its own, wrap it, and put the message
	// headers around the result.
	if req.Sign || req.Encrypt {
		var wrapped pgpBody
		if opt.memo != nil && opt.memo.done {
			wrapped = opt.memo.body
		} else {
			_, entity, err := composeEntity("", mail.Header{}, req)
			if err != nil {
				return "", nil, err
			}
			if wrapped, err = pgpWrap(opt.PGP, entity, req, opt.DraftCopy); err != nil {
				return "", nil, err
			}
			if opt.memo != nil {
				opt.memo.done, opt.memo.body = true, wrapped
			}
		}
		h.Set("MIME-Version", "1.0")
		h.SetContentType(wrapped.contentType, wrapped.params)
		var buf bytes.Buffer
		if err := textproto.WriteHeader(&buf, h.Header.Header); err != nil {
			return "", nil, err
		}
		buf.Write(wrapped.body)
		return msgID, buf.Bytes(), nil
	}
	return composeEntity(msgID, h, req)
}

// composeEntity writes the content of a message under the given header:
// the whole message when h holds the message headers, or just its MIME
// entity (for signing and encrypting) when h is empty.
func composeEntity(msgID string, h mail.Header, req SendRequest) (string, []byte, error) {
	body := strings.ReplaceAll(req.Body, "\r\n", "\n")
	// A Markdown body is sent as multipart/alternative: the text exactly as
	// written, and its rendering as HTML.
	htmlBody := ""
	if req.Markdown {
		var err error
		if htmlBody, err = email.MarkdownToHTML(body); err != nil {
			return "", nil, fmt.Errorf("render Markdown: %w", err)
		}
	}
	textHeader := func() mail.InlineHeader {
		var th mail.InlineHeader
		th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		return th
	}
	// writeAlternatives writes the body's versions into an alternative:
	// the text, its HTML rendering (Markdown), and a calendar object
	// (an answer to an invitation), in that order: readers pick the last
	// one they understand.
	alternatives := req.Markdown || req.Calendar != nil
	writeAlternatives := func(iw *mail.InlineWriter) error {
		type alt struct {
			header mail.InlineHeader
			data   string
		}
		parts := []alt{{textHeader(), body}}
		if req.Markdown {
			var hh mail.InlineHeader
			hh.SetContentType("text/html", map[string]string{"charset": "utf-8"})
			parts = append(parts, alt{hh, htmlBody})
		}
		if req.Calendar != nil {
			method := strings.ToUpper(headerValue(req.Calendar.Method))
			for _, r := range method {
				if r < 'A' || r > 'Z' {
					return fmt.Errorf("invalid calendar method %q", method)
				}
			}
			var ch mail.InlineHeader
			ch.SetContentType("text/calendar", map[string]string{"charset": "utf-8", "method": method})
			parts = append(parts, alt{ch, string(req.Calendar.Data)})
		}
		for _, part := range parts {
			w, err := iw.CreatePart(part.header)
			if err != nil {
				return err
			}
			if _, err := w.Write([]byte(part.data)); err != nil {
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
		}
		return iw.Close()
	}

	var buf bytes.Buffer
	if len(req.Attachments) == 0 {
		if alternatives {
			iw, err := mail.CreateInlineWriter(&buf, h)
			if err != nil {
				return "", nil, err
			}
			if err := writeAlternatives(iw); err != nil {
				return "", nil, err
			}
			return msgID, buf.Bytes(), nil
		}
		h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		w, err := mail.CreateSingleInlineWriter(&buf, h)
		if err != nil {
			return "", nil, err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return "", nil, err
		}
		if err := w.Close(); err != nil {
			return "", nil, err
		}
		return msgID, buf.Bytes(), nil
	}

	mw, err := mail.CreateWriter(&buf, h)
	if err != nil {
		return "", nil, err
	}
	if alternatives {
		iw, err := mw.CreateInline()
		if err != nil {
			return "", nil, err
		}
		if err := writeAlternatives(iw); err != nil {
			return "", nil, err
		}
	} else {
		tw, err := mw.CreateSingleInline(textHeader())
		if err != nil {
			return "", nil, err
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return "", nil, err
		}
		if err := tw.Close(); err != nil {
			return "", nil, err
		}
	}
	for _, a := range req.Attachments {
		var ah mail.AttachmentHeader
		ct := headerValue(a.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		ah.SetContentType(ct, nil)
		ah.SetFilename(headerValue(a.Filename))
		aw, err := mw.CreateAttachment(ah)
		if err != nil {
			return "", nil, err
		}
		if _, err := aw.Write(a.Data); err != nil {
			return "", nil, err
		}
		if err := aw.Close(); err != nil {
			return "", nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return "", nil, err
	}
	return msgID, buf.Bytes(), nil
}

// ComposeRFC5322 builds a plain-text message. Returns the Message-ID (without
// angle brackets) and the raw message bytes.
func ComposeRFC5322(from, to, subject, body string) (string, []byte) {
	id, raw, err := ComposeMessage(SendRequest{From: from, To: to, Subject: subject, Body: body}, ComposeOptions{})
	if err != nil {
		return "", nil
	}
	return id, raw
}

// setAddressHeader writes an address list header. Each entry is parsed so
// display names are RFC 2047 encoded; an entry that does not parse is written
// as sanitized text (legacy queued sends), never dropped silently.
func setAddressHeader(h *mail.Header, key string, entries []string) {
	var addrs []*mail.Address
	var raw []string
	for _, e := range entries {
		e = headerValue(e)
		if e == "" {
			continue
		}
		list, err := netmail.ParseAddressList(e)
		if err != nil {
			raw = append(raw, e)
			continue
		}
		for _, a := range list {
			addrs = append(addrs, (*mail.Address)(a))
		}
	}
	if len(raw) > 0 {
		var parts []string
		for _, a := range addrs {
			parts = append(parts, a.String())
		}
		h.Set(key, strings.Join(append(parts, raw...), ", "))
		return
	}
	if len(addrs) > 0 {
		h.SetAddressList(key, addrs)
	}
}

// cleanMsgID strips brackets and rejects values that are not a single msg-id
// token (Message-IDs of originals are third-party text).
func cleanMsgID(id string) string {
	id = strings.Trim(strings.TrimSpace(headerValue(id)), "<>")
	if id == "" || strings.ContainsAny(id, " \t<>\"") {
		return ""
	}
	return id
}

func cleanMsgIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if c := cleanMsgID(id); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// NewMessageID returns a fresh Message-ID (without brackets) whose domain
// part is taken from the sender address.
func NewMessageID(from string) string {
	var b [8]byte
	rand.Read(b[:])
	return fmt.Sprintf("%d.%s@%s", time.Now().UnixNano(), hex.EncodeToString(b[:]), sanitizeForMsgID(from))
}

// headerValue makes a string safe to place in a single header line: line
// breaks and control characters are removed.
func headerValue(s string) string {
	return strings.TrimSpace(email.SanitizeLine(s))
}

func sanitizeForMsgID(s string) string {
	// Extract just the email part for a clean Message-ID.
	if idx := strings.LastIndex(s, "@"); idx >= 0 {
		domain := s[idx+1:]
		if end := strings.IndexAny(domain, "> "); end >= 0 {
			domain = domain[:end]
		}
		if domain != "" {
			return domain
		}
	}
	return "localhost"
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// pgpMemo holds the wrapped content between two ComposeMessage calls for
// the same message.
type pgpMemo struct {
	done bool
	body pgpBody
}
