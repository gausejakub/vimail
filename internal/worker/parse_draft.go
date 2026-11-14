package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/emersion/go-message/mail"

	"github.com/gausejakub/vimail/internal/email"
)

// ParseDraftMessage turns a message from the server Drafts folder into a
// draft: recipients (including Bcc, which drafts keep), subject, plain-text
// body, threading headers, source and attachment content. The content was
// written by whatever client saved the draft, so every field is sanitized.
func ParseDraftMessage(raw []byte) (email.Draft, error) {
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

	body, err := ParseBody(raw)
	if err == nil && body.Text != "(no text content)" {
		d.Body = strings.TrimRight(body.Text, "\n")
	}
	parts, err := ExtractAttachmentData(raw)
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
