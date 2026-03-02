package worker

// Skeleton types for the IMAP/SMTP worker — no implementation yet.

type SyncRequest struct {
	Account string
	Folder  string
}

type SyncResult struct {
	Account  string
	Folder   string
	NewCount int
	Err      error
}

type SendRequest struct {
	From    string
	To      string
	Subject string
	Body    string
}

type SendResult struct {
	MessageID string
	Err       error
}
