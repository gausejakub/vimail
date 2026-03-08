package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
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

	mode           keys.Mode
	focusedPane    layout.Pane
	showHelp       bool
	showProcesses  bool
	showOps        bool
	attPicker      attachmentPicker
	layout         layout.SplitPaneLayout

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

// showProcessesMsg opens the processes overlay.
type showProcessesMsg struct{}

// showOpsMsg opens the operation log overlay.
type showOpsMsg struct{}

// attachmentPicker is the state for the attachment selection overlay.
type attachmentPicker struct {
	visible     bool
	attachments []email.Attachment
	selected    []bool
	cursor      int
	account     string
	folder      string
	uid         uint32
}

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
		compose:     compose.New(cfg.AI),
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

	// Clean up old completed/failed ops (older than 7 days).
	if sqlStore, ok := m.store.(*cache.SQLiteStore); ok {
		sqlStore.CleanupOldOps(7 * 24 * time.Hour)
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
		logging.Info("nav", "folder selected", logging.Acct(msg.Account), logging.Fld(msg.Folder))
		var cmd tea.Cmd
		m.msglist, cmd = m.msglist.Update(msg)
		cmds = append(cmds, cmd)
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		m.preview = m.preview.ClearMessage()
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
		logging.Debug("nav", "message selected", logging.MsgUID(msg.Message.UID), logging.KV("subject", msg.Message.Subject))
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		cmds = append(cmds, cmd)
		acct := m.mailbox.SelectedEmail()
		folder := m.msglist.CurrentFolder()
		// Lazy-fetch body if coordinator is available and body is empty or attachments not cached.
		needsFetch := msg.Message.Body == ""
		if !needsFetch {
			if sqlStore, ok := m.store.(*cache.SQLiteStore); ok {
				needsFetch = sqlStore.NeedsBodyRefetch(acct, folder, msg.Message.UID)
			}
		}
		if m.coordinator != nil && needsFetch && msg.Message.UID > 0 {
			cmds = append(cmds, m.coordinator.FetchBody(acct, folder, msg.Message.UID))
			var cmd tea.Cmd
			subject := msg.Message.Subject
			if len([]rune(subject)) > 30 {
				subject = string([]rune(subject)[:30]) + "…"
			}
			m.status, cmd = m.status.Update(util.ProcessStartMsg{ID: "fetch-body", Label: fmt.Sprintf("⟳ %s/%s: %s", acct, folder, subject)})
			cmds = append(cmds, cmd)
		}
		// Mark as read when focused.
		if msg.Message.Unread {
			m.store.MarkRead(acct, folder, msg.Message.ID)
			m.msglist = m.msglist.MarkReadByID(msg.Message.ID)
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
		logging.Info("draft", "saving draft", logging.KV("to", msg.To), logging.KV("subject", msg.Subject))
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
		logging.Info("send", "compose submitted", logging.KV("to", msg.To), logging.KV("subject", msg.Subject))
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
			var cmd tea.Cmd
			m.status, cmd = m.status.Update(util.ProcessStartMsg{ID: "send", Label: fmt.Sprintf("↑ %s: sending to %s", currentEmail, msg.To)})
			cmds = append(cmds, cmd)
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
		logging.Info("sync", "initial sync starting", logging.KV("accounts", len(m.cfg.Accounts)))
		m.syncPending = len(m.cfg.Accounts)
		for _, acct := range m.cfg.Accounts {
			m.msglist = m.msglist.SetAccountSyncing(acct.Email, true)
			m.mailbox = m.mailbox.SetAccountSyncing(acct.Email, true)
			email := acct.Email
			var cmd tea.Cmd
			m.status, cmd = m.status.Update(util.ProcessStartMsg{
				ID:    "sync:" + email,
				Label: "⟳ " + email + " connecting…",
			})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case worker.SyncProgressMsg:
		label := fmt.Sprintf("⟳ %s %d/%d %s", msg.Account, msg.Done, msg.Total, msg.Folder)
		if msg.Messages > 0 {
			label = fmt.Sprintf("⟳ %s %d/%d %s (%d msgs)", msg.Account, msg.Done, msg.Total, msg.Folder, msg.Messages)
		}
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessStartMsg{
			ID:    "sync:" + msg.Account,
			Label: label,
		})
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case worker.SyncAccountCompleteMsg:
		logging.Info("sync", "account sync complete (TUI)", logging.Acct(msg.Account), logging.Err(msg.Err), logging.KV("pending", m.syncPending-1))
		m.syncPending--
		m.mailbox = m.mailbox.SetAccountSyncing(msg.Account, false)
		m.msglist = m.msglist.SetAccountSyncing(msg.Account, false)
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessEndMsg{ID: "sync:" + msg.Account})
		cmds = append(cmds, cmd)
		// Reload mailbox sidebar with newly discovered folders.
		m.mailbox = m.mailbox.Reload()
		// Refresh current view if it belongs to this account.
		currentAcct := m.msglist.CurrentAccount()
		currentFolder := m.msglist.CurrentFolder()
		if msg.Account == currentAcct {
			cmds = append(cmds, func() tea.Msg {
				return util.FolderRefreshMsg{Account: currentAcct, Folder: currentFolder}
			})
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
		logging.Info("sync", "periodic sync tick")
		if m.coordinator != nil {
			m.syncPending = len(m.cfg.Accounts)
			for _, acct := range m.cfg.Accounts {
				m.mailbox = m.mailbox.SetAccountSyncing(acct.Email, true)
				m.msglist = m.msglist.SetAccountSyncing(acct.Email, true)
				email := acct.Email
				var cmd tea.Cmd
				m.status, cmd = m.status.Update(util.ProcessStartMsg{
					ID:    "sync:" + email,
					Label: "⟳ " + email + " connecting…",
				})
				cmds = append(cmds, cmd)
			}
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
		logging.Info("fetch", "body fetch result", logging.Acct(msg.Account), logging.Fld(msg.Folder), logging.MsgUID(msg.UID), logging.Err(msg.Err))
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessEndMsg{ID: "fetch-body"})
		cmds = append(cmds, cmd)
		if msg.Err == nil {
			bodyMsg := util.FetchBodyCompleteMsg{
				Account:     msg.Account,
				Folder:      msg.Folder,
				UID:         msg.UID,
				Body:        msg.Body,
				HTMLBody:    msg.HTMLBody,
				Attachments: msg.Attachments,
			}
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(bodyMsg)
			cmds = append(cmds, cmd)
			// Also update the message list so reply has the body.
			m.msglist = m.msglist.UpdateBody(msg.UID, msg.Body, msg.HTMLBody, msg.Attachments)
		} else {
			// Update preview with error so it doesn't show "(loading...)" forever.
			errBody := util.FetchBodyCompleteMsg{
				Account: msg.Account,
				Folder:  msg.Folder,
				UID:     msg.UID,
				Body:    "(failed to load body)",
			}
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(errBody)
			cmds = append(cmds, cmd)
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Fetch body: " + msg.Err.Error(), IsError: true}
			})
		}
		return m, tea.Batch(cmds...)

	case util.SaveAttachmentsResultMsg:
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessEndMsg{ID: "save-attachments"})
		cmds = append(cmds, cmd)
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "Save failed: " + msg.Err.Error(), IsError: true}
			})
		} else {
			dir := msg.Dir
			n := msg.Count
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: fmt.Sprintf("%d attachments saved to %s", n, dir), IsError: false}
			})
		}
		return m, tea.Batch(cmds...)

	case worker.SendResult:
		logging.Info("send", "send result", logging.Err(msg.Err))
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessEndMsg{ID: "send"})
		cmds = append(cmds, cmd)
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
		logging.Info("delete", "single delete requested", logging.Acct(msg.Account), logging.Fld(msg.Folder), logging.MsgUID(msg.Message.UID))
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
				var cmd tea.Cmd
				m.status, cmd = m.status.Update(util.ProcessStartMsg{ID: "delete", Label: fmt.Sprintf("⊘ %s/%s: deleting…", acct, folder)})
				cmds = append(cmds, cmd)
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
		logging.Info("delete", "batch delete requested", logging.Acct(msg.Account), logging.Fld(msg.Folder), logging.KV("count", len(msg.Messages)), logging.KV("select_all", msg.SelectAll))
		acct := msg.Account
		folder := msg.Folder
		if folder == "Drafts" {
			for _, message := range msg.Messages {
				m.store.DeleteDraft(acct, message.ID)
			}
		} else {
			var ids []string
			var uids []uint32
			// If selection covers the entire folder, get all UIDs from cache.
			if msg.SelectAll {
				if cs, ok := m.store.(*cache.SQLiteStore); ok {
					uids = cs.AllUIDs(acct, folder)
					for _, uid := range uids {
						ids = append(ids, fmt.Sprintf("%d", uid))
					}
				}
			}
			if len(ids) == 0 {
				for _, message := range msg.Messages {
					ids = append(ids, message.ID)
					if message.UID > 0 {
						uids = append(uids, message.UID)
					}
				}
			}
			m.store.DeleteMessages(acct, folder, ids)
			if m.coordinator != nil && len(uids) > 0 {
				cmds = append(cmds, m.coordinator.DeleteMessages(acct, folder, uids))
			}
		}
		n := len(msg.Messages)
		if msg.SelectAll {
			n = m.msglist.TotalCount()
		}
		if folder == "Drafts" {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: fmt.Sprintf("%d drafts deleted", n), IsError: false}
			})
		} else if m.coordinator != nil {
			var cmd tea.Cmd
			m.status, cmd = m.status.Update(util.ProcessStartMsg{
				ID:    "delete",
				Label: fmt.Sprintf("⊘ %s/%s: deleting %d msgs", acct, folder, n),
			})
			cmds = append(cmds, cmd)
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
		logging.Info("mark_read", "batch mark read requested", logging.Acct(msg.Account), logging.Fld(msg.Folder), logging.KV("count", len(msg.Messages)), logging.KV("select_all", msg.SelectAll))
		acct := msg.Account
		folder := msg.Folder
		n := 0
		if msg.SelectAll {
			if cs, ok := m.store.(*cache.SQLiteStore); ok {
				cs.MarkAllRead(acct, folder)
				uids := cs.AllUIDs(acct, folder)
				n = len(uids)
				if m.coordinator != nil {
					for _, uid := range uids {
						cmds = append(cmds, m.coordinator.MarkRead(acct, folder, uid))
					}
				}
			}
		} else {
			for _, message := range msg.Messages {
				if message.Unread {
					n++
					m.store.MarkRead(acct, folder, message.ID)
					if m.coordinator != nil && message.UID > 0 {
						cmds = append(cmds, m.coordinator.MarkRead(acct, folder, message.UID))
					}
				}
			}
		}
		// Reload message list + mailbox sidebar from the store.
		cmds = append(cmds, func() tea.Msg {
			return util.FolderRefreshMsg{Account: acct, Folder: folder}
		})
		cmds = append(cmds, func() tea.Msg {
			return util.InfoMsg{Text: fmt.Sprintf("%d messages marked as read", n), IsError: false}
		})
		return m, tea.Batch(cmds...)

	case worker.DeleteProgressMsg:
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessStartMsg{
			ID:    "delete",
			Label: fmt.Sprintf("⊘ %s/%s: deleting %d/%d", msg.Account, msg.Folder, msg.Done, msg.Total),
		})
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case worker.DeleteResult:
		logging.Info("delete", "delete result", logging.Acct(msg.Account), logging.Fld(msg.Folder), logging.Err(msg.Err))
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessEndMsg{ID: "delete"})
		cmds = append(cmds, cmd)
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

	case util.DeleteFolderRequestMsg:
		logging.Info("delete_folder", "folder delete requested", logging.Acct(msg.Account), logging.Fld(msg.Folder))
		acct := msg.Account
		folder := msg.Folder
		if m.coordinator != nil {
			cmds = append(cmds, m.coordinator.DeleteFolder(acct, folder))
			var cmd tea.Cmd
			m.status, cmd = m.status.Update(util.ProcessStartMsg{
				ID:    "delfolder",
				Label: fmt.Sprintf("⊘ %s: deleting folder %s", acct, folder),
			})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case util.DeleteFolderCompleteMsg:
		logging.Info("delete_folder", "folder delete result", logging.Acct(msg.Account), logging.Fld(msg.Folder), logging.Err(msg.Err))
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(util.ProcessEndMsg{ID: "delfolder"})
		cmds = append(cmds, cmd)
		if msg.Err != nil {
			errText := "Delete folder failed: " + msg.Err.Error()
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: errText, IsError: true}
			})
		} else {
			m.mailbox = m.mailbox.Reload()
			// Switch to Inbox if we were viewing the deleted folder.
			if m.msglist.CurrentFolder() == msg.Folder && m.msglist.CurrentAccount() == msg.Account {
				cmds = append(cmds, func() tea.Msg {
					return util.FolderSelectedMsg{Account: msg.Account, Folder: "Inbox"}
				})
			}
			folder := msg.Folder
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: fmt.Sprintf("Folder %q deleted", folder), IsError: false}
			})
		}
		return m, tea.Batch(cmds...)

	case util.ConnectionStatusMsg:
		logging.Info("connect", "connection status changed", logging.Acct(msg.Account), logging.KV("connected", msg.Connected), logging.Err(msg.Err))
		var cmd tea.Cmd
		m.status, cmd = m.status.Update(msg)
		cmds = append(cmds, cmd)
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: msg.Account + ": disconnected", IsError: true}
			})
		}
		return m, tea.Batch(cmds...)

	case showProcessesMsg:
		m.showProcesses = true
		return m, nil

	case showOpsMsg:
		m.showOps = true
		return m, nil

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

		// Attachment picker overlay
		if m.attPicker.visible {
			return m.handleAttachmentPickerKey(msg)
		}

		// Processes overlay
		if m.showProcesses {
			if msg.String() == "esc" || msg.String() == "q" {
				m.showProcesses = false
			}
			return m, nil
		}

		// Ops log overlay
		if m.showOps {
			if msg.String() == "esc" || msg.String() == "q" {
				m.showOps = false
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

	// Forward to msglist (e.g. async folder load results)
	var cmd tea.Cmd
	m.msglist, cmd = m.msglist.Update(msg)
	cmds = append(cmds, cmd)

	// Pass through other messages to status bar
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
		} else if k == "S" {
			// Save attachments for current message
			if sel := m.msglist.SelectedMessage(); sel != nil && len(sel.Attachments) > 0 {
				acct := m.mailbox.SelectedEmail()
				folder := m.msglist.CurrentFolder()
				if m.coordinator != nil {
					if len(sel.Attachments) == 1 {
						// Single attachment — save directly.
						var cmd tea.Cmd
						m.status, cmd = m.status.Update(util.ProcessStartMsg{
							ID:    "save-attachments",
							Label: fmt.Sprintf("⇣ %s/%s: saving %s", acct, folder, sel.Attachments[0].Filename),
						})
						cmds = append(cmds, cmd)
						cmds = append(cmds, m.coordinator.SaveAttachments(acct, folder, sel.UID, sel.Attachments))
					} else {
						// Multiple — show picker.
						selected := make([]bool, len(sel.Attachments))
						m.attPicker = attachmentPicker{
							visible:     true,
							attachments: sel.Attachments,
							selected:    selected,
							cursor:      0,
							account:     acct,
							folder:      folder,
							uid:         sel.UID,
						}
					}
				}
			} else {
				cmds = append(cmds, func() tea.Msg {
					return util.InfoMsg{Text: "No attachments", IsError: false}
				})
			}
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
		selectAll := m.msglist.VisualCoversAll()
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
					Account:   acct,
					Folder:    folder,
					Messages:  selected,
					SelectAll: selectAll,
				}
			})
		}

	case "r":
		selected := m.msglist.SelectedMessages()
		selectAll := m.msglist.VisualCoversAll()
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
					Account:   acct,
					Folder:    folder,
					Messages:  selected,
					SelectAll: selectAll,
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
	logging.Info("command", "executing command", logging.KV("cmd", parts[0]), logging.KV("args", strings.Join(parts[1:], " ")))

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

	case "processes", "ps":
		return func() tea.Msg { return showProcessesMsg{} }

	case "ops", "operations", "queue":
		return func() tea.Msg { return showOpsMsg{} }


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

	if m.showProcesses {
		screen = layout.PlaceOverlayCentered(m.processesView(), screen, m.width, m.height)
	}

	if m.showOps {
		screen = layout.PlaceOverlayCentered(m.opsView(), screen, m.width, m.height)
	}

	if m.attPicker.visible {
		screen = layout.PlaceOverlayCentered(m.attachmentPickerView(), screen, m.width, m.height)
	}

	if m.compose.Visible() {
		composeView := m.compose.View()
		screen = layout.PlaceOverlayCentered(composeView, screen, m.width, m.height)
	}

	return screen
}

func (m Model) processesView() string {
	t := theme.Current()

	title := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render("  Running Processes")

	var rows []string
	rows = append(rows, title)
	rows = append(rows, "")

	procs := m.status.Processes()
	if len(procs) == 0 {
		rows = append(rows, lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Render("  No active processes"))
	} else {
		for _, label := range procs {
			rows = append(rows, lipgloss.NewStyle().
				Foreground(t.Info()).
				Render("  "+label))
		}
	}

	rows = append(rows, "")
	rows = append(rows, lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Render("  Press Esc or q to close"))

	content := strings.Join(rows, "\n")

	// Size to content: find widest row + padding (4) + border (2).
	contentWidth := lipgloss.Width(content)
	w := contentWidth + 6
	if w < 30 {
		w = 30
	}
	if m.width > 0 && w > m.width-4 {
		w = m.width - 4
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BorderFocused()).
		Padding(1, 2).
		Width(w).
		Render(content)
}

func (m Model) opsView() string {
	t := theme.Current()

	title := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render("  Operation Queue")

	var rows []string
	rows = append(rows, title)
	rows = append(rows, "")

	var ops []cache.QueuedOp
	if sqlStore, ok := m.store.(*cache.SQLiteStore); ok {
		ops = sqlStore.RecentOps(20)
	}

	if len(ops) == 0 {
		rows = append(rows, lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Render("  No operations"))
	} else {
		for _, op := range ops {
			icon := "  "
			var color lipgloss.Color
			switch op.Status {
			case cache.OpPending:
				icon = "◯"
				color = t.TextMuted()
			case cache.OpRunning:
				icon = "◐"
				color = t.Info()
			case cache.OpCompleted:
				icon = "✓"
				color = t.Success()
			case cache.OpFailed:
				icon = "✗"
				color = t.Error()
			}

			desc := opDescription(op)
			age := formatAge(op.CreatedAt)
			line := fmt.Sprintf("  %s %s  %s", icon, desc, age)
			if op.Error != "" {
				errText := op.Error
				if len(errText) > 40 {
					errText = errText[:40] + "…"
				}
				line += "\n      " + errText
			}
			rows = append(rows, lipgloss.NewStyle().Foreground(color).Render(line))
		}
	}

	rows = append(rows, "")
	rows = append(rows, lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Render("  Press Esc or q to close"))

	content := strings.Join(rows, "\n")

	contentWidth := lipgloss.Width(content)
	w := contentWidth + 6
	if w < 40 {
		w = 40
	}
	maxW := m.width - 4
	if maxW < 40 {
		maxW = 40
	}
	if w > maxW {
		w = maxW
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BorderFocused()).
		Padding(1, 2).
		Width(w).
		Render(content)
}

func opDescription(op cache.QueuedOp) string {
	switch op.Type {
	case cache.OpDelete:
		var p cache.DeletePayload
		json.Unmarshal(op.Payload, &p)
		return fmt.Sprintf("delete %d msgs  %s/%s", len(p.UIDs), op.Account, op.Folder)
	case cache.OpSend:
		var p cache.SendPayload
		json.Unmarshal(op.Payload, &p)
		subj := p.Subject
		if len([]rune(subj)) > 25 {
			subj = string([]rune(subj)[:25]) + "…"
		}
		return fmt.Sprintf("send to %s: %s", p.To, subj)
	case cache.OpMarkRead:
		var p cache.MarkReadPayload
		json.Unmarshal(op.Payload, &p)
		return fmt.Sprintf("mark read %d msgs  %s/%s", len(p.UIDs), op.Account, op.Folder)
	default:
		return string(op.Type)
	}
}

func (m Model) handleAttachmentPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	k := msg.String()

	switch k {
	case "esc", "q":
		m.attPicker.visible = false

	case "j", "down":
		if m.attPicker.cursor < len(m.attPicker.attachments)-1 {
			m.attPicker.cursor++
		}

	case "k", "up":
		if m.attPicker.cursor > 0 {
			m.attPicker.cursor--
		}

	case " ":
		// Toggle selection
		m.attPicker.selected[m.attPicker.cursor] = !m.attPicker.selected[m.attPicker.cursor]
		// Move down after toggle
		if m.attPicker.cursor < len(m.attPicker.attachments)-1 {
			m.attPicker.cursor++
		}

	case "a":
		// Select all / deselect all
		allSelected := true
		for _, s := range m.attPicker.selected {
			if !s {
				allSelected = false
				break
			}
		}
		for i := range m.attPicker.selected {
			m.attPicker.selected[i] = !allSelected
		}

	case "enter":
		// Save selected attachments
		var chosen []email.Attachment
		for i, att := range m.attPicker.attachments {
			if m.attPicker.selected[i] {
				chosen = append(chosen, att)
			}
		}
		if len(chosen) == 0 {
			cmds = append(cmds, func() tea.Msg {
				return util.InfoMsg{Text: "No attachments selected", IsError: false}
			})
		} else if m.coordinator != nil {
			var cmd tea.Cmd
			m.status, cmd = m.status.Update(util.ProcessStartMsg{
				ID:    "save-attachments",
				Label: fmt.Sprintf("⇣ %s/%s: saving %d attachments", m.attPicker.account, m.attPicker.folder, len(chosen)),
			})
			cmds = append(cmds, cmd)
			cmds = append(cmds, m.coordinator.SaveAttachments(
				m.attPicker.account, m.attPicker.folder, m.attPicker.uid, chosen))
		}
		m.attPicker.visible = false
	}

	return m, tea.Batch(cmds...)
}

func (m Model) attachmentPickerView() string {
	t := theme.Current()

	title := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render("  Save Attachments")

	var rows []string
	rows = append(rows, title)
	rows = append(rows, "")

	for i, att := range m.attPicker.attachments {
		check := "[ ]"
		if m.attPicker.selected[i] {
			check = "[x]"
		}
		cursor := "  "
		color := t.Text()
		if i == m.attPicker.cursor {
			cursor = "> "
			color = t.Primary()
		}

		size := formatSize(att.Size)
		line := fmt.Sprintf("%s%s %s  %s", cursor, check, att.Filename, size)
		rows = append(rows, lipgloss.NewStyle().Foreground(color).Render(line))
	}

	rows = append(rows, "")
	rows = append(rows, lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Render("  Space: toggle  a: all  Enter: save  Esc: cancel"))

	content := strings.Join(rows, "\n")

	contentWidth := lipgloss.Width(content)
	w := contentWidth + 6
	if w < 40 {
		w = 40
	}
	maxW := m.width - 4
	if maxW < 40 {
		maxW = 40
	}
	if w > maxW {
		w = maxW
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BorderFocused()).
		Padding(1, 2).
		Width(w).
		Render(content)
}

func formatSize(bytes int) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
