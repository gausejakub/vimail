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
	ID          string
	UID         uint32
	From        string
	To          string
	Subject     string
	Body        string
	HTMLBody    string // raw HTML for "open in browser"
	Date        time.Time
	Unread      bool
	Flagged     bool
	Attachments []Attachment
	Folder      string // populated by search results for context
	Account     string // populated by search results for context
}

// Attachment represents a file attached to a message.
type Attachment struct {
	Filename    string
	ContentType string
	Size        int
	PartNum     string // MIME part number for IMAP fetch (e.g. "1.2")
}
