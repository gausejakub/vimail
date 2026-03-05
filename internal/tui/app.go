package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/components/compose"
	"github.com/gausejakub/vimail/internal/tui/components/help"
	"github.com/gausejakub/vimail/internal/tui/components/mailbox"
	"github.com/gausejakub/vimail/internal/tui/components/msglist"
	"github.com/gausejakub/vimail/internal/tui/components/preview"
	"github.com/gausejakub/vimail/internal/tui/components/status"
	"github.com/gausejakub/vimail/internal/tui/keys"
	"github.com/gausejakub/vimail/internal/tui/layout"
	"github.com/gausejakub/vimail/internal/tui/util"
	"github.com/gausejakub/vimail/internal/worker"
)

type Model struct {
	cfg         config.Config
	store       email.Store
	coordinator *worker.Coordinator // nil when using mock data

	width  int
	height int

	mode        keys.Mode
	focusedPane layout.Pane
	showHelp    bool
	layout      layout.SplitPaneLayout

	mailbox mailbox.Model
	msglist msglist.Model
	preview preview.Model
	status  status.Model
	compose compose.Model

	cmdInput  textinput.Model
	cmdActive bool

	syncPending int // number of accounts still syncing
}

// syncTickMsg triggers periodic sync refresh.
type syncTickMsg struct{}

// WithCoordinator sets the coordinator for real email connectivity.
func WithCoordinator(m Model, c *worker.Coordinator) Model {
	m.coordinator = c
	return m
}

func New(cfg config.Config, store email.Store) Model {
	theme.SetCurrent(cfg.Theme.Name)

	cmdInput := textinput.New()
	cmdInput.Prompt = ":"
	cmdInput.Placeholder = ""

	m := Model{
		cfg:         cfg,
		store:       store,
		mode:        keys.ModeNormal,
		focusedPane: layout.PaneMsgList,
		layout:      layout.SplitPaneLayout{ShowPreview: cfg.General.PreviewPane},
		mailbox:     mailbox.New(store),
		msglist:     msglist.New(store).Focus(),
		preview:     preview.New(),
		status:      status.New(),
		compose:     compose.New(),
		cmdInput:    cmdInput,
	}
	return m
}

func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd

	// Auto-select first message so preview isn't empty on launch.
	accts := m.store.Accounts()
	if len(accts) > 0 {
		msgs := m.store.MessagesFor(accts[0].Email, "Inbox")
		if len(msgs) > 0 {
			cmds = append(cmds, func() tea.Msg {
				return util.MessageSelectedMsg{Message: msgs[0]}
			})
		}
	}

	// Trigger initial sync if coordinator is available.
	if m.coordinator != nil {
		m.syncPending = len(m.cfg.Accounts)
		cmds = append(cmds, func() tea.Msg {
			return util.SyncStartMsg{}
		})
		cmds = append(cmds, m.coordinator.SyncAll())
	}

	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout.Resize(msg.Width, msg.Height, 1)
		m = m.resizePanes()
		m.status = m.status.SetWidth(msg.Width)

	case util.FolderSelectedMsg:
		var cmd tea.Cmd
		m.msglist, cmd = m.msglist.Update(msg)
		cmds = append(cmds, cmd)
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		// Auto-select first message in new folder
		msgs := m.store.MessagesFor(msg.Account, msg.Folder)
		if len(msgs) > 0 {
			cmds = append(cmds, func() tea.Msg {
				return util.MessageSelectedMsg{Message: msgs[0]}
			})
		} else {
			m.preview = m.preview.ClearMessage()
		}
		return m, tea.Batch(cmds...)

	case util.FolderRefreshMsg:
		var cmd tea.Cmd
		m.msglist, cmd = m.msglist.Update(msg)
		cmds = append(cmds, cmd)
		m.mailbox, cmd = m.mailbox.Update(msg)
		cmds = append(cmds, cmd)
		// Update preview for message now under cursor.
		if selected := m.msglist.SelectedMessage(); selected != nil {
			sel := *selected
			cmds = append(cmds, func() tea.Msg {
				return util.MessageSelectedMsg{Message: sel}
			})
		} else {
			m.preview = m.preview.ClearMessage()
		}
		return m, tea.Batch(cmds...)

	case util.MessageSelectedMsg:
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		cmds = append(cmds, cmd)
		acct := m.mailbox.SelectedEmail()
		folder := m.msglist.CurrentFolder()
		// Lazy-fetch body if coordinator is available and body is empty.
		if m.coordinator != nil && msg.Message.Body == "" && msg.Message.UID > 0 {
			cmds = append(cmds, m.coordinator.FetchBody(acct, folder, msg.Message.UID))
		}
		// Mark as read when focused.
		if msg.Message.Unread {
			m.store.MarkRead(acct, folder, msg.Message.ID)
			m.msglist = m.msglist.MarkCurrentRead()
			m.mailbox, _ = m.mailbox.Update(util.FolderRefreshMsg{Account: acct, Folder: folder})
			if m.coordinator != nil && msg.Message.UID > 0 {
				cmds = append(cmds, m.coordinator.MarkRead(acct, folder, msg.Message.UID))
			}
		}
		return m, tea.Batch(cmds...)

	case util.ComposeCloseMsg:
		m.compose = m.compose.Hide()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		return m, tea.Batch(cmds...)

	case util.ComposeSaveDraftMsg:
		m.compose = m.compose.Hide()
		m.mode = keys.ModeNormal
		currentEmail := m.mailbox.SelectedEmail()
		draftID := msg.DraftID
		if draftID == "" {
			draftID = m.store.NextDraftID()
		}
		m.store.SaveDraft(currentEmail, email.Message{
			ID:      draftID,
			From:    currentEmail,
			To:      msg.To,
			Subject: msg.Subject,
			Body:    msg.Body,
			Date:    time.Now(),
		})
		// Refresh msglist if currently viewing Drafts
		if m.msglist.CurrentFolder() == "Drafts" {
			cmds = append(cmds, func() tea.Msg {
				return util.FolderSelectedMsg{Account: currentEmail, Folder: "Drafts"}
			})
		}
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		cmds = append(cmds, func() tea.Msg {
			return util.InfoMsg{Text: "Draft saved", IsError: false}
		})
		return m, tea.Batch(cmds...)

	case util.OpenDraftMsg:
		draft := msg.Message
		m.compose = m.compose.ShowDraft(draft.ID, draft.To, draft.Subject, draft.Body).SetSize(m.width, m.height)
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		return m, tea.Batch(cmds...)

	case util.ComposeSubmitMsg:
		draftID := m.compose.DraftID()
		m.compose = m.compose.Hide()
		m.mode = keys.ModeNormal
		currentEmail := m.mailbox.SelectedEmail()
		// Remove draft if we were editing one.
		if draftID != "" {
			m.store.DeleteDraft(currentEmail, draftID)
		}
		if m.coordinator != nil {
			// Real send via SMTP.
			cmds = append(cmds, m.coordinator.SendAndArchive(currentEmail, worker.SendRequest{
				From:    currentEmail,
				To:      msg.To,
				Subject: msg.Subject,
				Body:    msg.Body,
			}))
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Sending…", IsError: false}
			})
		} else {
			// Mock send.
			m.mailbox = m.mailbox.SelectFolder(currentEmail, "Sent")
			cmds = append(cmds, func() tea.Msg {
				return util.FolderSelectedMsg{Account: currentEmail, Folder: "Sent"}
			})
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Message sent (mock)", IsError: false}
			})
		}
		return m, tea.Batch(cmds...)

	case util.InfoMsg:
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case keys.ModeChangedMsg:
		m.mode = msg.Mode
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case util.SyncStartMsg:
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		m.syncPending = len(m.cfg.Accounts)
		m.msglist = m.msglist.SetSyncing(true)
		for _, acct := range m.cfg.Accounts {
			m.mailbox = m.mailbox.SetAccountSyncing(acct.Email, true)
		}
		return m, tea.Batch(cmds...)

	case worker.SyncAccountCompleteMsg:
		m.syncPending--
		m.mailbox = m.mailbox.SetAccountSyncing(msg.Account, false)
		// Reload mailbox sidebar with newly discovered folders.
		m.mailbox = m.mailbox.Reload()
		// Refresh current view if it belongs to this account.
		acctEmail := m.mailbox.SelectedEmail()
		folder := m.msglist.CurrentFolder()
		if msg.Account == acctEmail || m.msglist.CurrentAccount() == msg.Account {
			cmds = append(cmds, func() tea.Msg {
				return util.FolderSelectedMsg{Account: acctEmail, Folder: folder}
			})
		}
		// If current folder is still empty, try to find one with messages.
		currentMsgs := m.store.MessagesFor(acctEmail, folder)
		if len(currentMsgs) == 0 {
			for _, acct := range m.store.Accounts() {
				if am := m.store.MessagesFor(acct.Email, "Inbox"); len(am) > 0 {
					acctEmail = acct.Email
					folder = "Inbox"
					m.mailbox = m.mailbox.SelectFolder(acctEmail, folder)
					cmds = append(cmds, func() tea.Msg {
						return util.FolderSelectedMsg{Account: acctEmail, Folder: folder}
					})
					break
				}
			}
		}
		if msg.Err != nil {
			errText := msg.Err.Error()
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: errText, IsError: true}
			})
		}
		// All accounts done?
		if m.syncPending <= 0 {
			m.msglist = m.msglist.SetSyncing(false)
			var cmd tea.Cmd
			m.status, cmd = m.status.Update(util.SyncAllCompleteMsg{})
			cmds = append(cmds, cmd)
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Sync complete", IsError: false}
			})
			// Schedule periodic refresh (5 minutes).
			cmds = append(cmds, tea.Tick(5*time.Minute, func(time.Time) tea.Msg {
				return syncTickMsg{}
			}))
		}
		return m, tea.Batch(cmds...)

	case syncTickMsg:
		if m.coordinator != nil {
			cmds = append(cmds, m.coordinator.SyncAll())
		}
		return m, tea.Batch(cmds...)

	case worker.SyncResult:
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Sync error: " + msg.Err.Error(), IsError: true}
			})
		}
		// Refresh if we're viewing this folder.
		if m.msglist.CurrentAccount() == msg.Account && m.msglist.CurrentFolder() == msg.Folder {
			cmds = append(cmds, func() tea.Msg {
				return util.FolderSelectedMsg{Account: msg.Account, Folder: msg.Folder}
			})
		}
		return m, tea.Batch(cmds...)

	case worker.FetchBodyResult:
		if msg.Err == nil {
			bodyMsg := util.FetchBodyCompleteMsg{
				Account:  msg.Account,
				Folder:   msg.Folder,
				UID:      msg.UID,
				Body:     msg.Body,
				HTMLBody: msg.HTMLBody,
			}
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(bodyMsg)
			cmds = append(cmds, cmd)
			// Also update the message list so reply has the body.
			m.msglist = m.msglist.UpdateBody(msg.UID, msg.Body, msg.HTMLBody)
		} else {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Fetch body: " + msg.Err.Error(), IsError: true}
			})
		}
		return m, tea.Batch(cmds...)

	case worker.SendResult:
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Send failed: " + msg.Err.Error(), IsError: true}
			})
		} else {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Message sent", IsError: false}
			})
			// Sync the Sent folder so the message appears.
			acctEmail := m.mailbox.SelectedEmail()
			if m.coordinator != nil && acctEmail != "" {
				cmds = append(cmds, m.coordinator.SyncFolder(acctEmail, "Sent"))
			}
		}
		return m, tea.Batch(cmds...)

	case util.DeleteRequestMsg:
		acct := msg.Account
		folder := msg.Folder
		if folder == "Drafts" {
			m.store.DeleteDraft(acct, msg.Message.ID)
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Draft deleted", IsError: false}
			})
		} else {
			m.store.DeleteMessage(acct, folder, msg.Message.ID)
			if m.coordinator != nil && msg.Message.UID > 0 {
				cmds = append(cmds, m.coordinator.DeleteMessage(acct, folder, msg.Message.UID))
				cmds = append(cmds, func() tea.Msg {
					return util.InfoMsg{Text: "Deleting…", IsError: false}
				})
			} else {
				cmds = append(cmds, func() tea.Msg {
					return util.InfoMsg{Text: "Message deleted", IsError: false}
				})
			}
		}
		cmds = append(cmds, func() tea.Msg {
			return util.FolderRefreshMsg{Account: acct, Folder: folder}
		})
		return m, tea.Batch(cmds...)

	case util.BatchDeleteRequestMsg:
		acct := msg.Account
		folder := msg.Folder
		if folder == "Drafts" {
			for _, message := range msg.Messages {
				m.store.DeleteDraft(acct, message.ID)
			}
		} else {
			var uids []uint32
			for _, message := range msg.Messages {
				m.store.DeleteMessage(acct, folder, message.ID)
				if message.UID > 0 {
					uids = append(uids, message.UID)
				}
			}
			if m.coordinator != nil && len(uids) > 0 {
				cmds = append(cmds, m.coordinator.DeleteMessages(acct, folder, uids))
			}
		}
		n := len(msg.Messages)
		if folder == "Drafts" {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: fmt.Sprintf("%d drafts deleted", n), IsError: false}
			})
		} else if m.coordinator != nil {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: fmt.Sprintf("Deleting %d messages…", n), IsError: false}
			})
		} else {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: fmt.Sprintf("%d messages deleted", n), IsError: false}
			})
		}
		cmds = append(cmds, func() tea.Msg {
			return util.FolderRefreshMsg{Account: acct, Folder: folder}
		})
		return m, tea.Batch(cmds...)

	case util.BatchMarkReadRequestMsg:
		acct := msg.Account
		folder := msg.Folder
		for _, message := range msg.Messages {
			if message.Unread {
				m.store.MarkRead(acct, folder, message.ID)
				if m.coordinator != nil && message.UID > 0 {
					cmds = append(cmds, m.coordinator.MarkRead(acct, folder, message.UID))
				}
			}
		}
		n := 0
		for _, message := range msg.Messages {
			if message.Unread {
				n++
			}
		}
		m.mailbox, _ = m.mailbox.Update(util.FolderRefreshMsg{Account: acct, Folder: folder})
		cmds = append(cmds, func() tea.Msg {
			return util.InfoMsg{Text: fmt.Sprintf("%d messages marked as read", n), IsError: false}
		})
		return m, tea.Batch(cmds...)

	case worker.DeleteResult:
		if msg.Err != nil {
			errText := "IMAP delete failed: " + msg.Err.Error()
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: errText, IsError: true}
			})
		} else {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Deleted on server", IsError: false}
			})
		}
		return m, tea.Batch(cmds...)

	case util.ConnectionStatusMsg:
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: msg.Account + ": disconnected", IsError: true}
			})
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		// Compose overlay eats all keys when visible
		if m.compose.Visible() {
			var cmd tea.Cmd
			m.compose, cmd = m.compose.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// Help overlay
		if m.showHelp {
			if msg.String() == "?" || msg.String() == "esc" {
				m.showHelp = false
			}
			return m, nil
		}

		// Command mode
		if m.cmdActive {
			return m.handleCommandKey(msg)
		}

		// Visual mode
		if m.mode == keys.ModeVisual {
			return m.handleVisualKey(msg)
		}

		// Normal mode
		return m.handleNormalKey(msg)
	}

	// Forward non-key messages to compose when visible (e.g. cursor blink)
	if m.compose.Visible() {
		var cmd tea.Cmd
		m.compose, cmd = m.compose.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Pass through other messages to status bar
	var cmd tea.Cmd
	m.status, cmd = m.status.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, keys.Normal.Quit):
		return m, tea.Quit

	case key.Matches(msg, keys.Normal.Help):
		m.showHelp = true

	case key.Matches(msg, keys.Normal.Command):
		m.cmdActive = true
		m.cmdInput.SetValue("")
		m.cmdInput.Focus()
		m.mode = keys.ModeCommand
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeCommand}
		})

	case key.Matches(msg, keys.Normal.Compose):
		m.compose = m.compose.Show().SetSize(m.width, m.height)
		m.mode = keys.ModeInsert
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeInsert}
		})
		cmds = append(cmds, textinput.Blink)

	case key.Matches(msg, keys.Normal.Reply):
		if selected := m.msglist.SelectedMessage(); selected != nil {
			// Extract email from "Name <email>" format
			replyTo := selected.From
			if idx := strings.Index(replyTo, "<"); idx >= 0 {
				end := strings.Index(replyTo, ">")
				if end > idx {
					replyTo = replyTo[idx+1 : end]
				}
			}
			// Build reply subject
			replySubj := selected.Subject
			if !strings.HasPrefix(strings.ToLower(replySubj), "re:") {
				replySubj = "Re: " + replySubj
			}
			// Quoted body as editor content
			quoted := "\n> " + strings.ReplaceAll(selected.Body, "\n", "\n> ")
			m.compose = m.compose.ShowReply(replyTo, replySubj, quoted).SetSize(m.width, m.height)
			m.mode = keys.ModeNormal
			cmds = append(cmds, func() tea.Msg {
				return keys.ModeChangedMsg{Mode: keys.ModeNormal}
			})
		}

	case key.Matches(msg, keys.Normal.Enter):
		if m.focusedPane == layout.PaneMsgList {
			if selected := m.msglist.SelectedMessage(); selected != nil {
				if m.msglist.CurrentFolder() == "Drafts" {
					draft := *selected
					cmds = append(cmds, func() tea.Msg {
						return util.OpenDraftMsg{Message: draft}
					})
					return m, tea.Batch(cmds...)
				}
			}
		}

	case key.Matches(msg, keys.Normal.Visual):
		if m.focusedPane == layout.PaneMsgList {
			m.msglist = m.msglist.EnterVisual()
			m.mode = keys.ModeVisual
			cmds = append(cmds, func() tea.Msg {
				return keys.ModeChangedMsg{Mode: keys.ModeVisual}
			})
		}

	case key.Matches(msg, keys.Normal.NextPane):
		m = m.cycleFocus(1)

	case key.Matches(msg, keys.Normal.PrevPane):
		m = m.cycleFocus(-1)

	default:
		// h/l at app level for pane switching
		k := msg.String()
		if k == "l" || k == "right" {
			m = m.cycleFocus(1)
		} else if k == "h" || k == "left" {
			m = m.cycleFocus(-1)
		} else if k == "o" {
			// Open in browser works from any pane
			var cmd tea.Cmd
			m.preview, cmd = m.preview.HandleKey(k)
			cmds = append(cmds, cmd)
		} else {
			// Forward to focused pane
			var cmd tea.Cmd
			switch m.focusedPane {
			case layout.PaneMailbox:
				m.mailbox, cmd = m.mailbox.HandleKey(k)
			case layout.PaneMsgList:
				m.msglist, cmd = m.msglist.HandleKey(k)
			case layout.PanePreview:
				m.preview, cmd = m.preview.HandleKey(k)
			}
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleVisualKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	k := msg.String()

	switch k {
	case "d":
		selected := m.msglist.SelectedMessages()
		lo, _ := m.msglist.VisualRange()
		m.msglist = m.msglist.ExitVisual()
		m.msglist = m.msglist.SetCursor(lo)
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		if len(selected) > 0 {
			acct := m.msglist.CurrentAccount()
			folder := m.msglist.CurrentFolder()
			cmds = append(cmds, func() tea.Msg {
				return util.BatchDeleteRequestMsg{
					Account:  acct,
					Folder:   folder,
					Messages: selected,
				}
			})
		}

	case "r":
		selected := m.msglist.SelectedMessages()
		lo, hi := m.msglist.VisualRange()
		m.msglist = m.msglist.MarkReadRange(lo, hi)
		m.msglist = m.msglist.ExitVisual()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		if len(selected) > 0 {
			acct := m.msglist.CurrentAccount()
			folder := m.msglist.CurrentFolder()
			cmds = append(cmds, func() tea.Msg {
				return util.BatchMarkReadRequestMsg{
					Account:  acct,
					Folder:   folder,
					Messages: selected,
				}
			})
		}

	case "esc", "v", "V":
		m.msglist = m.msglist.ExitVisual()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})

	default:
		// Forward movement keys to msglist (j, k, G, gg, etc.)
		var cmd tea.Cmd
		m.msglist, cmd = m.msglist.HandleKey(k)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, keys.Command.Cancel):
		m.cmdActive = false
		m.cmdInput.Blur()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})

	case key.Matches(msg, keys.Command.Submit):
		cmd := m.executeCommand(m.cmdInput.Value())
		m.cmdActive = false
		m.cmdInput.Blur()
		m.mode = keys.ModeNormal
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeNormal}
		})
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	default:
		var cmd tea.Cmd
		m.cmdInput, cmd = m.cmdInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) executeCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "quit", "q":
		return tea.Quit

	case "theme":
		if len(parts) < 2 {
			return func() tea.Msg {
				return util.InfoMsg{Text: "Usage: :theme <name>", IsError: true}
			}
		}
		name := parts[1]
		theme.SetCurrent(name)
		return func() tea.Msg {
			return util.InfoMsg{Text: "Theme: " + name, IsError: false}
		}

	case "sync":
		if m.coordinator != nil {
			return tea.Batch(
				func() tea.Msg { return util.SyncStartMsg{} },
				m.coordinator.SyncAll(),
			)
		}
		return func() tea.Msg {
			return util.InfoMsg{Text: "Sync not available (using mock data)", IsError: false}
		}

	default:
		return func() tea.Msg {
			return util.InfoMsg{Text: "Unknown command: " + parts[0], IsError: true}
		}
	}
}

func (m Model) cycleFocus(dir int) Model {
	m = m.blurAll()

	maxPane := 2
	if !m.layout.ShowPreview {
		maxPane = 1
	}

	next := (int(m.focusedPane) + dir + maxPane + 1) % (maxPane + 1)
	m.focusedPane = layout.Pane(next)

	switch m.focusedPane {
	case layout.PaneMailbox:
		m.mailbox = m.mailbox.Focus()
	case layout.PaneMsgList:
		m.msglist = m.msglist.Focus()
	case layout.PanePreview:
		m.preview = m.preview.Focus()
	}
	return m
}

func (m Model) blurAll() Model {
	m.mailbox = m.mailbox.Blur()
	m.msglist = m.msglist.Blur()
	m.preview = m.preview.Blur()
	return m
}

func (m Model) resizePanes() Model {
	ml := m.layout
	// Components get inner dimensions (minus 2 for border on each axis)
	m.mailbox = m.mailbox.SetSize(max(0, ml.MailboxWidth-2), max(0, ml.PaneHeight-2))
	m.msglist = m.msglist.SetSize(max(0, ml.MsgListWidth-2), max(0, ml.PaneHeight-2))
	if ml.ShowPreview {
		m.preview = m.preview.SetSize(max(0, ml.PreviewWidth-2), max(0, ml.PaneHeight-2))
	}
	m.compose = m.compose.SetSize(m.width, m.height)
	return m
}

func (m Model) View() string {
	// Render panes inside containers
	mailboxView := layout.Container{
		Width:   m.layout.MailboxWidth,
		Height:  m.layout.PaneHeight,
		Focused: m.focusedPane == layout.PaneMailbox,
		Border:  layout.BorderRounded,
	}.Render(m.mailbox.View())

	msglistView := layout.Container{
		Width:   m.layout.MsgListWidth,
		Height:  m.layout.PaneHeight,
		Focused: m.focusedPane == layout.PaneMsgList,
		Border:  layout.BorderRounded,
	}.Render(m.msglist.View())

	var previewView string
	if m.layout.ShowPreview {
		previewView = layout.Container{
			Width:   m.layout.PreviewWidth,
			Height:  m.layout.PaneHeight,
			Focused: m.focusedPane == layout.PanePreview,
			Border:  layout.BorderRounded,
		}.Render(m.preview.View())
	}

	panes := m.layout.Compose(mailboxView, msglistView, previewView)

	// Status bar or command input
	var bottomBar string
	if m.cmdActive {
		bottomBar = m.cmdInput.View()
	} else {
		bottomBar = m.status.View()
	}

	screen := panes + "\n" + bottomBar

	// Overlays
	if m.showHelp {
		helpView := help.View(m.width)
		screen = layout.PlaceOverlayCentered(helpView, screen, m.width, m.height)
	}

	if m.compose.Visible() {
		composeView := m.compose.View()
		screen = layout.PlaceOverlayCentered(composeView, screen, m.width, m.height)
	}

	return screen
}
