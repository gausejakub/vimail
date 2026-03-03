package worker

import (
	"fmt"
	"log"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
)

// Coordinator manages IMAP and SMTP workers for all configured accounts.
type Coordinator struct {
	cfg   config.Config
	store *cache.SQLiteStore

	mu      sync.Mutex
	imap    map[string]*IMAPWorker // keyed by email
	smtp    map[string]*SMTPWorker // keyed by email
	creds   map[string]*auth.Credentials
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
func (c *Coordinator) SyncAll() tea.Cmd {
	return func() tea.Msg {
		var wg sync.WaitGroup
		var mu sync.Mutex
		var errs []error

		for _, acct := range c.cfg.Accounts {
			acct := acct
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := c.syncAccount(acct); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("%s: %w", acct.Email, err))
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		return SyncAllCompleteMsg{Errors: errs}
	}
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
			Account:  acctEmail,
			Folder:   folder,
			UID:      uid,
			Body:     result.Text,
			HTMLBody: result.HTML,
			Err:      err,
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

		err := w.MoveToTrashBatch(folder, uids)
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
			if err := w.MoveToTrashBatch(pd.Folder, pd.UIDs); err != nil {
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
		if err := w.MoveToTrashBatch(pd.Folder, pd.UIDs); err != nil {
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

	// Sync each folder.
	for _, folder := range folders {
		if _, err := w.SyncFolder(folder); err != nil {
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
