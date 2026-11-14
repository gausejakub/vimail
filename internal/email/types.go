package email

import "time"

// Account represents an email account.
type Account struct {
	Name  string
	Email string
}

// Folder represents a mailbox folder.
type Folder struct {
	Name        string
	UnreadCount int
}

// Message represents an email message.
type Message struct {
	ID        string
	UID       uint32
	MessageID string // RFC 5322 Message-ID header (for cross-folder correlation)
	From      string
	To        string // display string: every To recipient, comma-joined
	Subject   string
	// Full recipient lists, one "Name <addr>" entry per address. ToList is
	// the parsed form of To; Bcc is only known for messages the user wrote
	// (Sent, Drafts).
	ToList  []string
	Cc      []string
	Bcc     []string
	ReplyTo []string
	// Threading headers (Message-IDs without angle brackets).
	InReplyTo  string
	References []string
	ThreadID   string // root Message-ID of the conversation (see ThreadIDFor)
	// DeliveredTo holds Delivered-To / X-Original-To addresses: which of the
	// user's addresses the message reached when it was not in To or Cc.
	DeliveredTo []string
	Body        string
	HTMLBody    string // raw HTML for "open in browser"
	Date        time.Time
	Unread      bool
	Flagged     bool
	Answered    bool
	// DraftConflict is, for a draft, the id of another draft that looks like
	// a second version of it (see Draft.ConflictWith).
	DraftConflict string
	Attachments   []Attachment
	Folder        string // populated by search results for context
	Account       string // populated by search results for context
}

// Attachment represents a file attached to a message.
type Attachment struct {
	Filename    string
	ContentType string
	Size        int
	PartNum     string // MIME part number for IMAP fetch (e.g. "1.2")
}
