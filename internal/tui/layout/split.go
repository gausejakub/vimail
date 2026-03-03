package layout

import "strings"

// Pane identifies each column in the 3-pane layout.
type Pane int

const (
	PaneMailbox Pane = iota
	PaneMsgList
	PanePreview
)

// SplitPaneLayout manages the widths and heights of the 3-pane layout.
type SplitPaneLayout struct {
	TotalWidth  int
	TotalHeight int
	ShowPreview bool

	MailboxWidth int
	MsgListWidth int
	PreviewWidth int
	PaneHeight   int
}

// Resize recalculates column widths for the given terminal dimensions.
// statusBarHeight is subtracted from the total height.
func (s *SplitPaneLayout) Resize(w, h, statusBarHeight int) {
	s.TotalWidth = w
	s.TotalHeight = h
	s.PaneHeight = max(0, h-statusBarHeight)

	s.MailboxWidth = 24
	if w > 160 {
		s.MailboxWidth = 28
	} else if w < 80 {
		s.MailboxWidth = 18
	}

	remaining := w - s.MailboxWidth
	if s.ShowPreview {
		s.PreviewWidth = w * 35 / 100
		s.MsgListWidth = remaining - s.PreviewWidth
	} else {
		s.PreviewWidth = 0
		s.MsgListWidth = remaining
	}
}

// Compose joins the three panes horizontally by directly concatenating
// corresponding lines. This avoids lipgloss.JoinHorizontal which can
// mismeasure ANSI-styled content and add unwanted padding.
func (s SplitPaneLayout) Compose(mailbox, msglist, preview string) string {
	mLines := strings.Split(mailbox, "\n")
	mlLines := strings.Split(msglist, "\n")

	var pLines []string
	if s.ShowPreview {
		pLines = strings.Split(preview, "\n")
	}

	var b strings.Builder
	for i := 0; i < s.PaneHeight; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < len(mLines) {
			b.WriteString(mLines[i])
		}
		if i < len(mlLines) {
			b.WriteString(mlLines[i])
		}
		if s.ShowPreview && i < len(pLines) {
			b.WriteString(pLines[i])
		}
	}
	return b.String()
}
