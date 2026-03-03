package worker

import "github.com/gausejakub/vimail/internal/email"

// SyncRequest requests syncing a specific folder.
type SyncRequest struct {
	Account string
	Folder  string
}

// SyncResult is the result of a folder sync.
type SyncResult struct {
	Account  string
	Folder   string
	NewCount int
	Err      error
}

// SendRequest is a request to send an email via SMTP.
type SendRequest struct {
	From    string
	To      string
	Subject string
	Body    string
}

// SendResult is the result of sending an email.
type SendResult struct {
	MessageID string
	Err       error
}

// FetchBodyRequest requests lazy-loading a message body.
type FetchBodyRequest struct {
	Account string
	Folder  string
	UID     uint32
}

// FetchBodyResult is the result of fetching a message body.
type FetchBodyResult struct {
	Account  string
	Folder   string
	UID      uint32
	Body     string
	HTMLBody string
	Err      error
}

// SyncAllCompleteMsg signals that initial sync of all accounts is done.
type SyncAllCompleteMsg struct {
	Errors []error
}

// NewMailMsg signals that new mail arrived via IDLE.
type NewMailMsg struct {
	Account string
	Folder  string
	Count   int
}

// ConnectionStatusMsg reports the connection state of an account.
type ConnectionStatusMsg struct {
	Account   string
	Connected bool
	Err       error
}

// FolderListResult holds the folders discovered for an account.
type FolderListResult struct {
	Account string
	Folders []email.Folder
}

// DeleteResult is the result of deleting a message via IMAP.
type DeleteResult struct {
	Account string
	Folder  string
	UID     uint32
	Err     error
}
