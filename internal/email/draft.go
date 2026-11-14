package email

import "time"

// Draft is an unsent message being prepared by the user (in the composer) or
// by an MCP client (save_draft). Everything the send path needs is here, so a
// draft survives the round trip save → edit → send without losing fields.
type Draft struct {
	ID      string
	Account string
	// From is the sending identity ("Name <addr>" or a bare address). Empty
	// means the account's primary address.
	From    string
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	Body    string
	// Threading headers (Message-IDs without angle brackets).
	InReplyTo  string
	References []string
	// Source is the message this draft replies to or forwards; on send the
	// original gets \Answered (reply) or $Forwarded (forward).
	Source      *DraftSource
	Attachments []DraftAttachment
	Date        time.Time

	// Server copy in the account's Drafts folder (read-only for callers).
	ServerMessageID string
	ServerUID       uint32
	// Synced reports that the server copy matches the current local version.
	Synced bool
}

// Draft source kinds.
const (
	SourceReply   = "reply"
	SourceForward = "forward"
)

// DraftSource identifies the original of a reply or forward.
type DraftSource struct {
	Kind      string `json:"kind"`
	Folder    string `json:"folder"`
	UID       uint32 `json:"uid"`
	MessageID string `json:"message_id,omitempty"`
}

// DraftAttachment is a file attached to a draft. The content is captured when
// it is attached, so what is reviewed is exactly what gets sent.
type DraftAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	// Size is len(Data); listings fill Size and leave Data nil.
	Size int    `json:"size,omitempty"`
	Data []byte `json:"data,omitempty"`
}

// AttachmentSize returns the attachment's size whether or not Data is loaded.
func (a DraftAttachment) AttachmentSize() int {
	if a.Data != nil {
		return len(a.Data)
	}
	return a.Size
}

// AsMessage returns the draft in the shape used by message lists.
func (d Draft) AsMessage() Message {
	return Message{
		ID:         d.ID,
		Account:    d.Account,
		Folder:     "Drafts",
		From:       d.From,
		To:         JoinAddresses(d.To),
		ToList:     d.To,
		Cc:         d.Cc,
		Bcc:        d.Bcc,
		Subject:    d.Subject,
		Body:       d.Body,
		Date:       d.Date,
		MessageID:  d.ServerMessageID,
		InReplyTo:  d.InReplyTo,
		References: d.References,
		ThreadID:   ThreadIDFor(d.ServerMessageID, d.InReplyTo, d.References),
		Attachments: func() []Attachment {
			var out []Attachment
			for _, a := range d.Attachments {
				out = append(out, Attachment{Filename: a.Filename, ContentType: a.ContentType, Size: a.AttachmentSize()})
			}
			return out
		}(),
	}
}
