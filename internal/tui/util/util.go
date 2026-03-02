package util

import "github.com/gause/vmail/internal/mock"

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
	Message mock.Message
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
	Message mock.Message
}
