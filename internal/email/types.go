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
	ID       string
	UID      uint32
	From     string
	To       string
	Subject  string
	Body     string
	HTMLBody string // raw HTML for "open in browser"
	Date     time.Time
	Unread   bool
	Flagged  bool
}
