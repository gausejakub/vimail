package worker

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// Coordinator manages IMAP and SMTP workers for all configured accounts.
type Coordinator struct {
	cfg   config.Config
	store *cache.SQLiteStore

	mu      sync.Mutex
	imap    map[string]*IMAPWorker // keyed by email
	smtp    map[string]*SMTPWorker // keyed by email
	creds   map[string]*auth.Credentials

	program *tea.Program // set after bubbletea starts, for async progress messages
}

// NewCoordinator creates a coordinator for the given config and store.
func NewCoordinator(cfg config.Config, store *cache.SQLiteStore) *Coordinator {
	return &Coordinator{
		cfg:   cfg,
		store: store,
		imap:  make(map[string]*IMAPWorker),
		smtp:  make(map[string]*SMTPWorker),
		creds: make(map[string]*auth.Credentials),
	}
}

// SetProgram sets the bubbletea program reference for sending async progress messages.
func (c *Coordinator) SetProgram(p *tea.Program) {
	c.program = p
}

// ResolveCredentials resolves and caches credentials for all accounts.
// Should be called before SyncAll.
func (c *Coordinator) ResolveCredentials() []error {
	var errs []error
	for _, acct := range c.cfg.Accounts {
		resolver := auth.NewResolver(acct)
		creds, err := resolver.Resolve(acct)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", acct.Email, err))
			continue
		}
		c.mu.Lock()
		c.creds[acct.Email] = creds
		c.mu.Unlock()
	}
	return errs
}

// SyncAll returns a tea.Cmd that syncs all accounts concurrently.
// Each account reports its own completion via SyncAccountCompleteMsg,
// and a final SyncAllCompleteMsg is sent when all are done.
func (c *Coordinator) SyncAll() tea.Cmd {
	var cmds []tea.Cmd

	for _, acct := range c.cfg.Accounts {
		acct := acct
		cmds = append(cmds, func() tea.Msg {
			var syncErr error
			if err := c.syncAccount(acct); err != nil {
				syncErr = fmt.Errorf("%s: %w", acct.Email, err)
			}
			return SyncAccountCompleteMsg{
				Account: acct.Email,
				Err:     syncErr,
			}
		})
	}

	return tea.Batch(cmds...)
}

// SyncFolder returns a tea.Cmd that syncs a specific folder.
func (c *Coordinator) SyncFolder(acctEmail, folder string) tea.Cmd {
	return func() tea.Msg {
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			return SyncResult{
				Account: acctEmail,
				Folder:  folder,
				Err:     fmt.Errorf("no IMAP worker for %s", acctEmail),
			}
		}

		newCount, err := w.SyncFolder(folder)
		return SyncResult{
			Account:  acctEmail,
			Folder:   folder,
			NewCount: newCount,
			Err:      err,
		}
	}
}

// FetchBody returns a tea.Cmd that lazily fetches a message body.
func (c *Coordinator) FetchBody(acctEmail, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			return FetchBodyResult{
				Account: acctEmail,
				Folder:  folder,
				UID:     uid,
				Err:     fmt.Errorf("no IMAP worker for %s", acctEmail),
			}
		}

		result, err := w.FetchBody(folder, uid)
		return FetchBodyResult{
			Account:     acctEmail,
			Folder:      folder,
			UID:         uid,
			Body:        result.Text,
			HTMLBody:    result.HTML,
			Attachments: result.Attachments,
			Err:         err,
		}
	}
}

// MarkRead returns a tea.Cmd that marks a message as read on the IMAP server.
// The operation is queued so it can be retried if the connection is lost.
func (c *Coordinator) MarkRead(acctEmail, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		opID, _ := c.store.QueueOp(cache.OpMarkRead, acctEmail, folder, cache.MarkReadPayload{UIDs: []uint32{uid}})
		c.store.StartOp(opID)

		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			c.store.FailOp(opID, "no IMAP worker")
			return nil
		}
		w.MarkRead(folder, uid)
		c.store.CompleteOp(opID)
		return nil
	}
}

// SaveAttachments fetches the raw message and saves the specified attachments to ~/Downloads.
// Only attachments whose filename matches one in the provided list are saved.
func (c *Coordinator) SaveAttachments(acctEmail, folder string, uid uint32, attachments []email.Attachment) tea.Cmd {
	return func() tea.Msg {
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			return util.SaveAttachmentsResultMsg{Err: fmt.Errorf("no IMAP worker for %s", acctEmail)}
		}

		raw, err := w.FetchRawMessage(folder, uid)
		if err != nil {
			return util.SaveAttachmentsResultMsg{Err: fmt.Errorf("fetch message: %w", err)}
		}

		parts, err := ExtractAttachmentData(raw)
		if err != nil {
			return util.SaveAttachmentsResultMsg{Err: fmt.Errorf("parse attachments: %w", err)}
		}

		// Build a set of wanted filenames to filter by.
		wanted := make(map[string]bool, len(attachments))
		for _, a := range attachments {
			wanted[a.Filename] = true
		}

		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, "Downloads")
		os.MkdirAll(dir, 0755)

		saved := 0
		for _, att := range parts {
			if !wanted[att.Filename] {
				continue
			}
			path := filepath.Join(dir, att.Filename)
			// Avoid overwriting: append (1), (2), etc.
			path = uniquePath(path)
			if err := os.WriteFile(path, att.Data, 0644); err != nil {
				log.Printf("save attachment %s: %v", att.Filename, err)
				continue
			}
			saved++
		}

		return util.SaveAttachmentsResultMsg{Count: saved, Dir: dir}
	}
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}

// SendAndArchive returns a tea.Cmd that sends an email via SMTP and
// appends it to the Sent folder via IMAP.
// The operation is queued so it can be retried on reconnect.
func (c *Coordinator) SendAndArchive(acctEmail string, req SendRequest) tea.Cmd {
	return func() tea.Msg {
		opID, _ := c.store.QueueOp(cache.OpSend, acctEmail, "", cache.SendPayload{
			From: req.From, To: req.To, Subject: req.Subject, Body: req.Body,
		})
		c.store.StartOp(opID)

		smtpW := c.getSMTPWorker(acctEmail)
		if smtpW == nil {
			c.store.FailOp(opID, fmt.Sprintf("no SMTP worker for %s", acctEmail))
			return SendResult{Err: fmt.Errorf("no SMTP worker for %s", acctEmail)}
		}

		msgID, sentMsg, err := smtpW.Send(req)
		if err != nil {
			c.store.FailOp(opID, err.Error())
			return SendResult{Err: err}
		}

		// Append to Sent folder via IMAP.
		imapW := c.getIMAPWorker(acctEmail)
		if imapW != nil && sentMsg != nil {
			if err := imapW.AppendToFolder("Sent", sentMsg, nil); err != nil {
				log.Printf("APPEND to Sent failed: %v", err)
			}
		}

		c.store.CompleteOp(opID)
		return SendResult{MessageID: msgID}
	}
}

// DeleteMessage returns a tea.Cmd that moves a message to Trash via IMAP.
func (c *Coordinator) DeleteMessage(acctEmail, folder string, uid uint32) tea.Cmd {
	return c.DeleteMessages(acctEmail, folder, []uint32{uid})
}

// DeleteMessages returns a tea.Cmd that moves multiple messages to Trash via IMAP in a single batch.
// The operation is queued so it can be retried on reconnect.
func (c *Coordinator) DeleteMessages(acctEmail, folder string, uids []uint32) tea.Cmd {
	return func() tea.Msg {
		opID, _ := c.store.QueueOp(cache.OpDelete, acctEmail, folder, cache.DeletePayload{UIDs: uids})
		c.store.StartOp(opID)

		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			c.store.FailOp(opID, fmt.Sprintf("no IMAP worker for %s", acctEmail))
			return DeleteResult{
				Account: acctEmail,
				Folder:  folder,
				Err:     fmt.Errorf("no IMAP worker for %s", acctEmail),
			}
		}

		var onProgress func(done, total int)
		if c.program != nil && len(uids) > 1 {
			onProgress = func(done, total int) {
				c.program.Send(DeleteProgressMsg{
					Account: acctEmail,
					Folder:  folder,
					Done:    done,
					Total:   total,
				})
			}
		}
		err := w.MoveToTrashBatch(folder, uids, onProgress)
		if err != nil {
			c.store.FailOp(opID, err.Error())
		} else {
			c.store.CompleteOp(opID)
			c.store.ClearPendingDeletes(acctEmail, folder, uids)
		}
		return DeleteResult{
			Account: acctEmail,
			Folder:  folder,
			Err:     err,
		}
	}
}

// RetryPendingOps retries any pending or failed operations from the queue.
func (c *Coordinator) RetryPendingOps() tea.Cmd {
	return func() tea.Msg {
		ops := c.store.PendingOps()
		for _, op := range ops {
			c.store.StartOp(op.ID)
			var err error
			switch op.Type {
			case cache.OpDelete:
				var payload cache.DeletePayload
				if e := json.Unmarshal(op.Payload, &payload); e != nil {
					c.store.FailOp(op.ID, "bad payload: "+e.Error())
					continue
				}
				w := c.getIMAPWorker(op.Account)
				if w == nil {
					c.store.FailOp(op.ID, "no IMAP worker")
					continue
				}
				err = w.MoveToTrashBatch(op.Folder, payload.UIDs, nil)
				if err == nil {
					c.store.ClearPendingDeletes(op.Account, op.Folder, payload.UIDs)
				}

			case cache.OpSend:
				var payload cache.SendPayload
				if e := json.Unmarshal(op.Payload, &payload); e != nil {
					c.store.FailOp(op.ID, "bad payload: "+e.Error())
					continue
				}
				smtpW := c.getSMTPWorker(op.Account)
				if smtpW == nil {
					c.store.FailOp(op.ID, "no SMTP worker")
					continue
				}
				_, sentMsg, sendErr := smtpW.Send(SendRequest{
					From: payload.From, To: payload.To,
					Subject: payload.Subject, Body: payload.Body,
				})
				if sendErr != nil {
					err = sendErr
				} else {
					imapW := c.getIMAPWorker(op.Account)
					if imapW != nil && sentMsg != nil {
						imapW.AppendToFolder("Sent", sentMsg, nil)
					}
				}

			case cache.OpMarkRead:
				var payload cache.MarkReadPayload
				if e := json.Unmarshal(op.Payload, &payload); e != nil {
					c.store.FailOp(op.ID, "bad payload: "+e.Error())
					continue
				}
				w := c.getIMAPWorker(op.Account)
				if w == nil {
					c.store.FailOp(op.ID, "no IMAP worker")
					continue
				}
				for _, uid := range payload.UIDs {
					w.MarkRead(op.Folder, uid)
				}
			}

			if err != nil {
				log.Printf("retry op %d (%s): %v", op.ID, op.Type, err)
				c.store.FailOp(op.ID, err.Error())
			} else {
				c.store.CompleteOp(op.ID)
			}
		}
		return nil
	}
}

// syncAccount connects and syncs all folders for a single account.
func (c *Coordinator) syncAccount(acct config.AccountConfig) error {
	if acct.IMAPHost == "" {
		return nil // No IMAP configured, skip.
	}

	c.mu.Lock()
	creds := c.creds[acct.Email]
	c.mu.Unlock()
	if creds == nil {
		return fmt.Errorf("no credentials resolved")
	}

	// Disconnect old worker if it exists to avoid leaking connections.
	c.mu.Lock()
	if old, ok := c.imap[acct.Email]; ok {
		old.Disconnect()
	}
	c.mu.Unlock()

	w := NewIMAPWorker(acct, creds, c.store)
	if err := w.Connect(); err != nil {
		return err
	}

	c.mu.Lock()
	c.imap[acct.Email] = w
	c.mu.Unlock()

	// Retry any pending operations for this account before syncing folders.
	ops := c.store.PendingOps()
	for _, op := range ops {
		if op.Account != acct.Email {
			continue
		}
		c.store.StartOp(op.ID)
		switch op.Type {
		case cache.OpDelete:
			var payload cache.DeletePayload
			if err := json.Unmarshal(op.Payload, &payload); err != nil {
				c.store.FailOp(op.ID, "bad payload: "+err.Error())
				continue
			}
			if err := w.MoveToTrashBatch(op.Folder, payload.UIDs, nil); err != nil {
				log.Printf("retry delete %s/%s: %v", op.Account, op.Folder, err)
				c.store.FailOp(op.ID, err.Error())
			} else {
				c.store.CompleteOp(op.ID)
				c.store.ClearPendingDeletes(op.Account, op.Folder, payload.UIDs)
			}
		case cache.OpMarkRead:
			var payload cache.MarkReadPayload
			if err := json.Unmarshal(op.Payload, &payload); err != nil {
				c.store.FailOp(op.ID, "bad payload: "+err.Error())
				continue
			}
			for _, uid := range payload.UIDs {
				w.MarkRead(op.Folder, uid)
			}
			c.store.CompleteOp(op.ID)
		default:
			// Send ops are retried separately.
		}
	}

	// List mailboxes.
	folders, err := w.ListMailboxes()
	if err != nil {
		return fmt.Errorf("list mailboxes: %w", err)
	}

	// Sync each folder with progress reporting.
	// Use STATUS pre-check to skip folders with no new messages.
	synced := 0
	for i, folder := range folders {
		if c.program != nil {
			c.program.Send(SyncProgressMsg{
				Account:  acct.Email,
				Folder:   folder,
				Done:     i,
				Total:    len(folders),
				Messages: 0,
			})
		}

		// Quick STATUS check: skip folder if UIDNEXT hasn't changed.
		uidNext, uidValidity, err := w.FolderStatus(folder)
		if err != nil {
			log.Printf("status %s/%s: %v", acct.Email, folder, err)
			continue
		}
		storedUV, _ := c.store.GetUIDValidity(acct.Email, folder)
		highUID, _ := c.store.HighestUID(acct.Email, folder)
		if storedUV == uidValidity && highUID > 0 && uidNext <= highUID+1 {
			log.Printf("skip %s/%s: no new messages (UIDNEXT=%d, highUID=%d)", acct.Email, folder, uidNext, highUID)
			continue
		}

		synced++
		var onProgress func(fetched int)
		if c.program != nil {
			folderCopy := folder
			idx := i
			total := len(folders)
			onProgress = func(fetched int) {
				c.program.Send(SyncProgressMsg{
					Account:  acct.Email,
					Folder:   folderCopy,
					Done:     idx,
					Total:    total,
					Messages: fetched,
				})
			}
		}
		if _, err := w.SyncFolder(folder, onProgress); err != nil {
			log.Printf("sync %s/%s: %v", acct.Email, folder, err)
		}
	}
	log.Printf("sync %s: %d/%d folders needed sync", acct.Email, synced, len(folders))

	// Also set up SMTP worker if configured.
	if acct.SMTPHost != "" {
		smtpW := NewSMTPWorker(acct, creds)
		c.mu.Lock()
		c.smtp[acct.Email] = smtpW
		c.mu.Unlock()
	}

	return nil
}

func (c *Coordinator) getIMAPWorker(acctEmail string) *IMAPWorker {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imap[acctEmail]
}

func (c *Coordinator) getSMTPWorker(acctEmail string) *SMTPWorker {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.smtp[acctEmail]
}

// DisconnectAll cleanly disconnects all workers.
func (c *Coordinator) DisconnectAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.imap {
		w.Disconnect()
	}
	c.imap = make(map[string]*IMAPWorker)
	c.smtp = make(map[string]*SMTPWorker)
}
