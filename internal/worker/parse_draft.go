package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/emersion/go-message/mail"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/pgp"
)

// ParseDraftMessage turns a message from the server Drafts folder into a
// draft: recipients (including Bcc, which drafts keep), subject, plain-text
// body, threading headers, source and attachment content. The content was
// written by whatever client saved the draft, so every field is sanitized.
func ParseDraftMessage(raw []byte) (email.Draft, error) {
	return ParseDraftMessagePGP(raw, nil)
}

// ParseDraftMessagePGP is ParseDraftMessage for a draft that may be
// encrypted (to its author): its content is decrypted with the client. An
// encrypted draft that cannot be decrypted is an error, never an empty draft.
func ParseDraftMessagePGP(raw []byte, client *pgp.Client) (email.Draft, error) {
	content := raw
	if mt, params := topContentType(raw); mt == "multipart/encrypted" && strings.EqualFold(params["protocol"], "application/pgp-encrypted") {
		if client == nil {
			return email.Draft{}, fmt.Errorf("the draft is encrypted and OpenPGP is not enabled")
		}
		plain, _, err := client.Decrypt(encryptedPayload(raw))
		if err != nil {
			return email.Draft{}, fmt.Errorf("decrypt draft: %w", err)
		}
		content = plain
	}
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return email.Draft{}, fmt.Errorf("parse draft: %w", err)
	}
	h := mr.Header
	var d email.Draft
	addrs := func(key string) []string {
		list, err := h.AddressList(key)
		if err != nil {
			return nil
		}
		var out []string
		for _, a := range list {
			out = append(out, email.FormatAddress(a.Name, a.Address))
		}
		return out
	}
	if from := addrs("From"); len(from) > 0 {
		d.From = from[0]
	}
	d.To, d.Cc, d.Bcc = addrs("To"), addrs("Cc"), addrs("Bcc")
	if s, err := h.Subject(); err == nil {
		d.Subject = email.SanitizeLine(s)
	}
	if t, err := h.Date(); err == nil {
		d.Date = t
	}
	if ids, err := h.MsgIDList("In-Reply-To"); err == nil && len(ids) > 0 {
		d.InReplyTo = ids[0]
	}
	if ids, err := h.MsgIDList("References"); err == nil {
		d.References = ids
	}
	if src := h.Get(draftSourceHeader); src != "" {
		var s email.DraftSource
		if json.Unmarshal([]byte(src), &s) == nil && (s.Kind == email.SourceReply || s.Kind == email.SourceForward) {
			s.Folder = email.SanitizeLine(s.Folder)
			d.Source = &s
		}
	}
	mr.Close()

	body, err := ParseBody(content)
	switch {
	case err != nil:
	case strings.TrimSpace(body.Plain) != "":
		// What the author typed, not a rendering of the HTML alternative
		// (for a Markdown draft that is the Markdown source).
		d.Body = strings.TrimRight(body.Plain, "\n")
	case body.Text != "(no text content)":
		d.Body = strings.TrimRight(body.Text, "\n")
	}
	d.Markdown = h.Get(draftMarkdownHeader) == "1"
	for _, flag := range strings.Split(h.Get(draftPGPHeader), ",") {
		switch strings.TrimSpace(flag) {
		case "sign":
			d.Sign = true
		case "encrypt":
			d.Encrypt = true
		}
	}
	parts, err := ExtractAttachmentData(content)
	if err == nil {
		for _, p := range parts {
			d.Attachments = append(d.Attachments, email.DraftAttachment{
				Filename:    email.SanitizeLine(p.Filename),
				ContentType: p.ContentType,
				Data:        p.Data,
				Size:        len(p.Data),
			})
		}
	}
	return d, nil
}

// draftSourceValue encodes a source for the X-Vimail-Source header.
func draftSourceValue(src *email.DraftSource) string {
	if src == nil {
		return ""
	}
	data, _ := json.Marshal(src)
	return string(data)
}
