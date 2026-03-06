package util

import "github.com/gausejakub/vimail/internal/email"

// InfoMsg is displayed in the status bar with an optional error flag.
type InfoMsg struct {
	Text    string
	IsError bool
}

// ThemeChangedMsg triggers a theme switch.
type ThemeChangedMsg struct {
	Name string
}

// SyncStatusMsg updates the sync indicator in the status bar.
type SyncStatusMsg struct {
	LastSyncAgo string
}

// FolderSelectedMsg is emitted when a folder is selected in the mailbox pane.
type FolderSelectedMsg struct {
	Account string
	Folder  string
}

// MessageSelectedMsg is emitted when a message is selected in the message list.
type MessageSelectedMsg struct {
	Message email.Message
}

// ComposeSubmitMsg is emitted when the compose overlay submits.
type ComposeSubmitMsg struct {
	To      string
	Subject string
	Body    string
}

// ComposeCloseMsg is emitted when the compose overlay should close without saving.
type ComposeCloseMsg struct{}

// ComposeSaveDraftMsg is emitted when compose closes and should save a draft.
type ComposeSaveDraftMsg struct {
	DraftID string // empty for new drafts
	To      string
	Subject string
	Body    string
}

// OpenDraftMsg requests opening a draft message in the compose overlay.
type OpenDraftMsg struct {
	Message email.Message
}

// SyncStartMsg signals that a sync operation has started.
type SyncStartMsg struct {
	Account string
}

// SyncCompleteMsg signals that a folder sync completed.
type SyncCompleteMsg struct {
	Account  string
	Folder   string
	NewCount int
	Err      error
}

// SyncAllCompleteMsg signals that initial sync of all accounts finished.
type SyncAllCompleteMsg struct {
	Errors []error
}

// SendStartMsg signals that a message send has started.
type SendStartMsg struct{}

// SendCompleteMsg signals that a message send completed.
type SendCompleteMsg struct {
	MessageID string
	Err       error
}

// FetchBodyCompleteMsg signals that a message body fetch completed.
type FetchBodyCompleteMsg struct {
	Account     string
	Folder      string
	UID         uint32
	Body        string
	HTMLBody    string
	Attachments []email.Attachment
	Err         error
}

// ConnectionStatusMsg reports connection state for an account.
type ConnectionStatusMsg struct {
	Account   string
	Connected bool
	Err       error
}

// NewMailMsg signals new mail arrived via IDLE.
type NewMailMsg struct {
	Account string
	Folder  string
	Count   int
}

// FolderRefreshMsg reloads messages for the current folder without resetting cursor.
type FolderRefreshMsg struct {
	Account string
	Folder  string
}

// DeleteRequestMsg is emitted by the message list when the user presses dd.
type DeleteRequestMsg struct {
	Account string
	Folder  string
	Message email.Message
}

// BatchDeleteRequestMsg is emitted by the message list in visual mode when the user presses d.
type BatchDeleteRequestMsg struct {
	Account  string
	Folder   string
	Messages []email.Message
}

// BatchMarkReadRequestMsg is emitted in visual mode when the user marks selected messages as read.
type BatchMarkReadRequestMsg struct {
	Account  string
	Folder   string
	Messages []email.Message
}

// SaveAttachmentsRequestMsg requests saving all attachments for the current message.
type SaveAttachmentsRequestMsg struct {
	Account     string
	Folder      string
	UID         uint32
	Attachments []email.Attachment
}

// SaveAttachmentsResultMsg reports the result of saving attachments.
type SaveAttachmentsResultMsg struct {
	Count int
	Dir   string
	Err   error
}

// DeleteCompleteMsg signals that a message delete completed.
type DeleteCompleteMsg struct {
	Account string
	Folder  string
	UID     uint32
	Err     error
}
