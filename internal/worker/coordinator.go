package worker

import (
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
func (c *Coordinator) MarkRead(acctEmail, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			return nil
		}
		w.MarkRead(folder, uid)
		return nil
	}
}

// SaveAttachments fetches the raw message and saves all attachments to ~/Downloads.
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

		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, "Downloads")
		os.MkdirAll(dir, 0755)

		saved := 0
		for _, att := range parts {
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
func (c *Coordinator) SendAndArchive(acctEmail string, req SendRequest) tea.Cmd {
	return func() tea.Msg {
		smtpW := c.getSMTPWorker(acctEmail)
		if smtpW == nil {
			return SendResult{Err: fmt.Errorf("no SMTP worker for %s", acctEmail)}
		}

		msgID, sentMsg, err := smtpW.Send(req)
		if err != nil {
			return SendResult{Err: err}
		}

		// Append to Sent folder via IMAP.
		imapW := c.getIMAPWorker(acctEmail)
		if imapW != nil && sentMsg != nil {
			if err := imapW.AppendToFolder("Sent", sentMsg, nil); err != nil {
				log.Printf("APPEND to Sent failed: %v", err)
			}
		}

		return SendResult{MessageID: msgID}
	}
}

// DeleteMessage returns a tea.Cmd that moves a message to Trash via IMAP.
func (c *Coordinator) DeleteMessage(acctEmail, folder string, uid uint32) tea.Cmd {
	return c.DeleteMessages(acctEmail, folder, []uint32{uid})
}

// DeleteMessages returns a tea.Cmd that moves multiple messages to Trash via IMAP in a single batch.
// Clears pending deletes on success so future syncs don't block them.
func (c *Coordinator) DeleteMessages(acctEmail, folder string, uids []uint32) tea.Cmd {
	return func() tea.Msg {
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			return DeleteResult{
				Account: acctEmail,
				Folder:  folder,
				Err:     fmt.Errorf("no IMAP worker for %s", acctEmail),
			}
		}

		var onProgress func(done, total int)
		if c.program != nil && len(uids) > 500 {
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
		if err == nil {
			c.store.ClearPendingDeletes(acctEmail, folder, uids)
		}
		return DeleteResult{
			Account: acctEmail,
			Folder:  folder,
			Err:     err,
		}
	}
}

// RetryPendingDeletes retries any pending deletions that failed previously.
func (c *Coordinator) RetryPendingDeletes() tea.Cmd {
	return func() tea.Msg {
		pending := c.store.PendingDeletes()
		for _, pd := range pending {
			w := c.getIMAPWorker(pd.Account)
			if w == nil {
				continue
			}
			if err := w.MoveToTrashBatch(pd.Folder, pd.UIDs, nil); err != nil {
				log.Printf("retry delete %s/%s: %v", pd.Account, pd.Folder, err)
				continue
			}
			c.store.ClearPendingDeletes(pd.Account, pd.Folder, pd.UIDs)
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

	// Retry any pending deletions before syncing folders.
	pending := c.store.PendingDeletes()
	for _, pd := range pending {
		if pd.Account != acct.Email {
			continue
		}
		if err := w.MoveToTrashBatch(pd.Folder, pd.UIDs, nil); err != nil {
			log.Printf("retry delete %s/%s: %v", pd.Account, pd.Folder, err)
		} else {
			c.store.ClearPendingDeletes(pd.Account, pd.Folder, pd.UIDs)
		}
	}

	// List mailboxes.
	folders, err := w.ListMailboxes()
	if err != nil {
		return fmt.Errorf("list mailboxes: %w", err)
	}

	// Sync each folder with progress reporting.
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
