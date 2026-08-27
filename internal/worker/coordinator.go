package worker

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// Coordinator manages IMAP and SMTP workers for all configured accounts.
type Coordinator struct {
	cfg   config.Config
	store *cache.SQLiteStore

	mu    sync.Mutex
	imap  map[string]*IMAPWorker // keyed by email
	smtp  map[string]*SMTPWorker // keyed by email
	creds map[string]*auth.Credentials
	// connectMu serializes lazy worker creation per account without blocking
	// independent accounts from connecting concurrently.
	connectMu map[string]*sync.Mutex

	// syncInFlight tracks coalesced per-folder recovery syncs, keyed by
	// account+"\x00"+folder, so a burst of stale-UID errors starts one sync.
	syncInFlight map[string]bool

	program *tea.Program // set after bubbletea starts, for async progress messages
}

// NewCoordinator creates a coordinator for the given config and store.
func NewCoordinator(cfg config.Config, store *cache.SQLiteStore) *Coordinator {
	return &Coordinator{
		cfg:          cfg,
		store:        store,
		imap:         make(map[string]*IMAPWorker),
		smtp:         make(map[string]*SMTPWorker),
		creds:        make(map[string]*auth.Credentials),
		connectMu:    make(map[string]*sync.Mutex),
		syncInFlight: make(map[string]bool),
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
		if err := c.ensureCredentials(acct); err != nil {
			logging.Error("auth", "credential resolution failed", logging.Acct(acct.Email), logging.Err(err))
			errs = append(errs, fmt.Errorf("%s: %w", acct.Email, err))
		}
	}
	return errs
}

// ensureCredentials resolves one account lazily. This lets standalone MCP
// writes establish their own worker without requiring a sync first.
func (c *Coordinator) ensureCredentials(acct config.AccountConfig) error {
	c.mu.Lock()
	creds := c.creds[acct.Email]
	if creds != nil {
		c.mu.Unlock()
		return nil
	}

	// Hold the coordinator lock through resolution so concurrent first writes
	// cannot prompt for or resolve the same account credentials repeatedly.
	resolver := auth.NewResolver(acct)
	resolved, err := resolver.Resolve(acct)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	logging.Debug("auth", "credentials resolved", logging.Acct(acct.Email))
	c.creds[acct.Email] = resolved
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) accountConnectMutex(account string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	mu := c.connectMu[account]
	if mu == nil {
		mu = &sync.Mutex{}
		c.connectMu[account] = mu
	}
	return mu
}

// SyncAll returns a tea.Cmd that syncs all accounts concurrently.
// Each account reports its own completion via SyncAccountCompleteMsg,
// and a final SyncAllCompleteMsg is sent when all are done.
func (c *Coordinator) SyncAll() tea.Cmd {
	logging.Info("sync", "starting sync for all accounts", logging.KV("count", len(c.cfg.Accounts)))
	var cmds []tea.Cmd

	for _, acct := range c.cfg.Accounts {
		acct := acct
		cmds = append(cmds, func() (result tea.Msg) {
			defer func() {
				if r := recover(); r != nil {
					logging.Error("sync", "panic during sync", logging.Acct(acct.Email), logging.KV("panic", fmt.Sprint(r)))
					result = SyncAccountCompleteMsg{
						Account: acct.Email,
						Err:     fmt.Errorf("%s: panic: %v", acct.Email, r),
					}
				}
			}()
			start := time.Now()

			// Run syncAccount with a timeout to prevent indefinite hangs.
			type syncResult struct {
				err error
			}
			ch := make(chan syncResult, 1)
			go func() {
				_, err := c.syncAccount(acct, false)
				ch <- syncResult{err: err}
			}()

			var syncErr error
			select {
			case res := <-ch:
				if res.err != nil {
					syncErr = fmt.Errorf("%s: %w", acct.Email, res.err)
					logging.Error("sync", "account sync failed", logging.Acct(acct.Email), logging.Dur(time.Since(start)), logging.Err(res.err))
				} else {
					logging.Info("sync", "account sync complete", logging.Acct(acct.Email), logging.Dur(time.Since(start)))
				}
			case <-time.After(5 * time.Minute):
				syncErr = fmt.Errorf("%s: sync timed out after 5 minutes", acct.Email)
				logging.Error("sync", "account sync timed out", logging.Acct(acct.Email), logging.Dur(time.Since(start)))
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
		logging.Info("sync", "single folder sync starting", logging.Acct(acctEmail), logging.Fld(folder))
		start := time.Now()
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			logging.Error("sync", "no IMAP worker", logging.Acct(acctEmail), logging.Fld(folder))
			return SyncResult{
				Account: acctEmail,
				Folder:  folder,
				Err:     fmt.Errorf("no IMAP worker for %s", acctEmail),
			}
		}

		// Serialize with other processes syncing this account (e.g. the MCP
		// server) — the HighestUID watermark and UIDVALIDITY purge race
		// otherwise. Skipping is safe: the holder is refreshing the same data.
		release, ok := c.store.TryAcquireSyncLock(acctEmail)
		if !ok {
			logging.Info("sync", "account sync locked by another process, skipping folder sync", logging.Acct(acctEmail), logging.Fld(folder))
			return SyncResult{Account: acctEmail, Folder: folder}
		}
		defer release()

		newCount, err := w.SyncFolder(folder)
		if err != nil {
			logging.Error("sync", "folder sync failed", logging.Acct(acctEmail), logging.Fld(folder), logging.Dur(time.Since(start)), logging.Err(err))
		} else {
			logging.Info("sync", "folder sync complete", logging.Acct(acctEmail), logging.Fld(folder), logging.Dur(time.Since(start)), logging.KV("new_count", newCount))
		}
		return SyncResult{
			Account:  acctEmail,
			Folder:   folder,
			NewCount: newCount,
			Err:      err,
		}
	}
}

// SyncFolderIfIdle returns a tea.Cmd that syncs the folder, or nil if a
// coalesced sync for the same account/folder pair is already in flight.
// Stale-UID recovery uses this so a rapid burst of missing-message errors
// triggers exactly one folder sync instead of one per UID. The in-flight
// mark is claimed synchronously (before the returned cmd runs) and released
// when the sync finishes, whether it succeeds or fails.
func (c *Coordinator) SyncFolderIfIdle(acctEmail, folder string) tea.Cmd {
	key := acctEmail + "\x00" + folder
	c.mu.Lock()
	if c.syncInFlight[key] {
		c.mu.Unlock()
		logging.Debug("sync", "recovery sync already in flight, coalescing", logging.Acct(acctEmail), logging.Fld(folder))
		return nil
	}
	c.syncInFlight[key] = true
	c.mu.Unlock()

	inner := c.SyncFolder(acctEmail, folder)
	return func() tea.Msg {
		// Release on every exit path so future recovery stays possible.
		defer func() {
			c.mu.Lock()
			delete(c.syncInFlight, key)
			c.mu.Unlock()
		}()
		return inner()
	}
}

// FetchBody returns a tea.Cmd that lazily fetches a message body.
func (c *Coordinator) FetchBody(acctEmail, folder string, uid uint32) tea.Cmd {
	return func() (result tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				logging.Error("fetch", "panic during body fetch", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid), logging.KV("panic", fmt.Sprint(r)))
				result = FetchBodyResult{
					Account: acctEmail,
					Folder:  folder,
					UID:     uid,
					Err:     fmt.Errorf("panic: %v", r),
				}
			}
		}()
		logging.Debug("fetch", "fetching body", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid))
		start := time.Now()
		res, err := c.FetchBodyNow(acctEmail, folder, uid)
		if err != nil {
			logging.Error("fetch", "body fetch failed", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid), logging.Dur(time.Since(start)), logging.Err(err))
		} else {
			logging.Debug("fetch", "body fetched", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid), logging.Dur(time.Since(start)))
		}
		return FetchBodyResult{
			Account:     acctEmail,
			Folder:      folder,
			UID:         uid,
			Body:        res.Text,
			HTMLBody:    res.HTML,
			Attachments: res.Attachments,
			Err:         err,
		}
	}
}

// FetchBodyNow synchronously fetches and caches one message body, connecting
// lazily when this coordinator has not synced the account yet. This is the
// non-Bubble Tea seam used by MCP batch reads.
func (c *Coordinator) FetchBodyNow(acctEmail, folder string, uid uint32) (BodyResult, error) {
	return c.fetchBodyNow(acctEmail, folder, uid, false)
}

// PeekBodyNow synchronously fetches and caches one message body without
// setting its IMAP \Seen flag. MCP review uses this path to remain read-only.
func (c *Coordinator) PeekBodyNow(acctEmail, folder string, uid uint32) (BodyResult, error) {
	return c.fetchBodyNow(acctEmail, folder, uid, true)
}

func (c *Coordinator) fetchBodyNow(acctEmail, folder string, uid uint32, peek bool) (BodyResult, error) {
	acct, ok := c.accountByEmail(acctEmail)
	if !ok {
		return BodyResult{}, fmt.Errorf("no such account: %s", acctEmail)
	}
	// Body fetches use their own reconnecting connection, so an existing
	// worker does not need an extra PING for every message in a batch.
	w, err := c.ensureIMAPWorkerMode(acct, false)
	if err != nil {
		return BodyResult{}, err
	}
	// Mark this UID as wanted before taking the worker's fetch lock. A newer
	// request can supersede a stale TUI fetch, while sequential MCP reads set
	// and fetch each UID together.
	w.SetWantedFetchUID(uid)
	if peek {
		return w.PeekBodyDirect(folder, uid)
	}
	return w.FetchBodyDirect(folder, uid)
}

// MarkRead returns a tea.Cmd that marks a message as read on the IMAP server.
// The operation is queued so it can be retried if the connection is lost.
func (c *Coordinator) MarkRead(acctEmail, folder string, uid uint32) tea.Cmd {
	return c.MarkReadMessages(acctEmail, folder, []uint32{uid})
}

// MarkReadMessages queues and executes one batched mark-read operation.
func (c *Coordinator) MarkReadMessages(acctEmail, folder string, uids []uint32) tea.Cmd {
	return func() tea.Msg {
		c.MarkReadMessagesNow(acctEmail, folder, uids)
		return nil
	}
}

// MarkReadMessagesNow is the synchronous seam used by MCP. It keeps the same
// durable queue semantics as the TUI command while returning delivery state.
func (c *Coordinator) MarkReadMessagesNow(acctEmail, folder string, uids []uint32) MarkReadResult {
	return c.markReadNow(acctEmail, folder, cache.MarkReadPayload{UIDs: uids})
}

// MarkAllRead returns the TUI command form of the authoritative whole-folder
// operation.
func (c *Coordinator) MarkAllRead(acctEmail, folder string) tea.Cmd {
	return func() tea.Msg {
		c.MarkAllReadNow(acctEmail, folder)
		return nil
	}
}

// MarkAllReadNow queues one authoritative whole-folder IMAP operation. Unlike
// UID batches, it also covers messages absent from a stale local cache.
func (c *Coordinator) MarkAllReadNow(acctEmail, folder string) MarkReadResult {
	return c.markReadNow(acctEmail, folder, cache.MarkReadPayload{All: true})
}

func (c *Coordinator) markReadNow(acctEmail, folder string, payload cache.MarkReadPayload) MarkReadResult {
	count := len(payload.UIDs)
	if payload.All {
		logging.Debug("mark_read", "marking all messages read", logging.Acct(acctEmail), logging.Fld(folder))
	} else {
		logging.Debug("mark_read", "marking messages read", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", count))
	}
	opID, err := c.store.QueueOp(cache.OpMarkRead, acctEmail, folder, payload)
	result := MarkReadResult{OpID: opID}
	if err != nil {
		result.Err = fmt.Errorf("queue mark-read: %w", err)
		return result
	}
	if !c.store.StartOp(opID) {
		return result // another process owns the durable operation
	}

	acct, ok := c.accountByEmail(acctEmail)
	if !ok {
		result.Err = fmt.Errorf("no such account: %s", acctEmail)
		c.store.FailOp(opID, result.Err.Error())
		return result
	}
	w, err := c.ensureIMAPWorkerForWrite(acct)
	if err != nil {
		result.Err = err
		logging.Warn("mark_read", "IMAP worker unavailable", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", count), logging.Err(err))
		c.store.FailOp(opID, err.Error())
		return result
	}
	if payload.All {
		err = w.MarkAllRead(folder)
	} else {
		err = w.MarkReadBatch(folder, payload.UIDs)
	}
	if err != nil {
		result.Err = err
		logging.Warn("mark_read", "IMAP mark read failed", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", count), logging.Err(err))
		c.store.FailOp(opID, err.Error())
		return result
	}
	c.store.CompleteOp(opID)
	result.Delivered = true
	return result
}

// SaveAttachments fetches the raw message and saves the specified attachments to ~/Downloads.
// Only attachments whose filename matches one in the provided list are saved.
func (c *Coordinator) SaveAttachments(acctEmail, folder string, uid uint32, attachments []email.Attachment) tea.Cmd {
	return func() tea.Msg {
		logging.Info("save", "saving attachments", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid), logging.KV("count", len(attachments)))
		start := time.Now()
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			logging.Error("save", "no IMAP worker", logging.Acct(acctEmail))
			return util.SaveAttachmentsResultMsg{Err: fmt.Errorf("no IMAP worker for %s", acctEmail)}
		}

		raw, err := w.FetchRawMessage(folder, uid)
		if err != nil {
			logging.Error("save", "fetch raw message failed", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid), logging.Err(err))
			return util.SaveAttachmentsResultMsg{Err: fmt.Errorf("fetch message: %w", err)}
		}

		parts, err := ExtractAttachmentData(raw)
		if err != nil {
			logging.Error("save", "parse attachments failed", logging.Acct(acctEmail), logging.Err(err))
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
		var dangerousNames []string
		for _, att := range parts {
			if !wanted[att.Filename] {
				continue
			}
			// Sanitize filename to prevent path traversal.
			safeName := filepath.Base(att.Filename)
			if safeName == "." || safeName == "/" || safeName == "" {
				safeName = "attachment"
			}
			if isDangerousFilename(safeName) {
				dangerousNames = append(dangerousNames, safeName)
			}
			path := filepath.Join(dir, safeName)
			// Avoid overwriting: append (1), (2), etc.
			path = uniquePath(path)
			if err := os.WriteFile(path, att.Data, 0600); err != nil {
				logging.Error("save", "write attachment failed", logging.KV("filename", att.Filename), logging.Err(err))
				continue
			}
			saved++
		}

		logging.Info("save", "attachments saved", logging.KV("saved", saved), logging.KV("dir", dir), logging.Dur(time.Since(start)))
		var warning string
		if len(dangerousNames) > 0 {
			warning = fmt.Sprintf("⚠ Potentially dangerous: %s", strings.Join(dangerousNames, ", "))
			logging.Warn("save", "dangerous attachment types saved", logging.KV("files", strings.Join(dangerousNames, ", ")))
		}
		return util.SaveAttachmentsResultMsg{Count: saved, Dir: dir, Warning: warning}
	}
}

// dangerousExts contains file extensions that may be executable or contain macros.
var dangerousExts = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".msi": true,
	".scr": true, ".pif": true, ".ps1": true, ".sh": true, ".bash": true,
	".js": true, ".vbs": true, ".wsf": true, ".hta": true, ".jar": true,
	".docm": true, ".xlsm": true, ".pptm": true,
	".iso": true, ".img": true, ".dmg": true,
	".lnk": true, ".url": true, ".webloc": true,
}

// isDangerousFilename checks if a filename has a dangerous extension,
// including double-extension tricks like "invoice.pdf.exe".
func isDangerousFilename(name string) bool {
	lower := strings.ToLower(name)
	ext := filepath.Ext(lower)
	if dangerousExts[ext] {
		return true
	}
	// Check for double extensions (e.g. "file.pdf.exe").
	base := strings.TrimSuffix(lower, ext)
	if ext2 := filepath.Ext(base); ext2 != "" && dangerousExts[ext2] {
		return true
	}
	return false
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
		logging.Info("send", "sending email", logging.Acct(acctEmail), logging.KV("to", req.To), logging.KV("subject", req.Subject))
		start := time.Now()
		opID, _ := c.store.QueueOp(cache.OpSend, acctEmail, "", cache.SendPayload{
			From: req.From, To: req.To, Subject: req.Subject, Body: req.Body,
		})
		if !c.store.StartOp(opID) {
			// Another process's drainer claimed the send — executing here too
			// would produce a duplicate outbound email.
			logging.Warn("send", "send op claimed by another process", logging.Acct(acctEmail), logging.KV("op_id", opID))
			return SendResult{MessageID: ""}
		}

		smtpW := c.getSMTPWorker(acctEmail)
		if smtpW == nil {
			logging.Error("send", "no SMTP worker", logging.Acct(acctEmail))
			c.store.FailOp(opID, fmt.Sprintf("no SMTP worker for %s", acctEmail))
			return SendResult{Err: fmt.Errorf("no SMTP worker for %s", acctEmail)}
		}

		msgID, sentMsg, err := smtpW.Send(req)
		if err != nil {
			logging.Error("send", "SMTP send failed", logging.Acct(acctEmail), logging.Dur(time.Since(start)), logging.Err(err))
			c.store.FailOp(opID, err.Error())
			return SendResult{Err: err}
		}

		// Append to Sent folder via IMAP.
		imapW := c.getIMAPWorker(acctEmail)
		if imapW != nil && sentMsg != nil {
			if err := imapW.AppendToFolder("Sent", sentMsg, nil); err != nil {
				logging.Warn("send", "IMAP APPEND to Sent failed", logging.Acct(acctEmail), logging.Err(err))
			}
		}

		c.store.CompleteOp(opID)
		logging.Info("send", "email sent", logging.Acct(acctEmail), logging.KV("message_id", msgID), logging.Dur(time.Since(start)))
		return SendResult{MessageID: msgID}
	}
}

// DeleteFolder returns a tea.Cmd that deletes a mailbox on the IMAP server and removes it from cache.
func (c *Coordinator) DeleteFolder(acctEmail, folder string) tea.Cmd {
	return func() tea.Msg {
		logging.Info("delete_folder", "deleting folder", logging.Acct(acctEmail), logging.Fld(folder))
		start := time.Now()
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			logging.Error("delete_folder", "no IMAP worker", logging.Acct(acctEmail), logging.Fld(folder))
			return util.DeleteFolderCompleteMsg{Account: acctEmail, Folder: folder, Err: fmt.Errorf("no IMAP worker for %s", acctEmail)}
		}

		if err := w.DeleteMailbox(folder); err != nil {
			logging.Error("delete_folder", "IMAP DELETE failed", logging.Acct(acctEmail), logging.Fld(folder), logging.Dur(time.Since(start)), logging.Err(err))
			return util.DeleteFolderCompleteMsg{Account: acctEmail, Folder: folder, Err: err}
		}

		c.store.DeleteFolder(acctEmail, folder)
		logging.Info("delete_folder", "folder deleted", logging.Acct(acctEmail), logging.Fld(folder), logging.Dur(time.Since(start)))
		return util.DeleteFolderCompleteMsg{Account: acctEmail, Folder: folder}
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
		logging.Info("delete", "deleting messages", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", len(uids)))
		start := time.Now()
		opID, _ := c.store.QueueOp(cache.OpDelete, acctEmail, folder, cache.DeletePayload{UIDs: uids})
		if !c.store.StartOp(opID) {
			// Another process's drainer claimed the op — it will execute it.
			return DeleteResult{Account: acctEmail, Folder: folder}
		}

		acct, ok := c.accountByEmail(acctEmail)
		if !ok {
			err := fmt.Errorf("no such account: %s", acctEmail)
			c.store.FailOp(opID, err.Error())
			return DeleteResult{
				Account: acctEmail,
				Folder:  folder,
				Err:     err,
			}
		}
		w, err := c.ensureIMAPWorkerForWrite(acct)
		if err != nil {
			logging.Error("delete", "IMAP worker unavailable", logging.Acct(acctEmail), logging.Fld(folder), logging.Err(err))
			c.store.FailOp(opID, err.Error())
			return DeleteResult{Account: acctEmail, Folder: folder, Err: err}
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
		err = w.MoveToTrashBatch(folder, uids, onProgress)
		if err != nil {
			logging.Error("delete", "batch delete failed", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", len(uids)), logging.Dur(time.Since(start)), logging.Err(err))
			c.store.FailOp(opID, err.Error())
		} else {
			c.store.CompleteOp(opID)
			c.store.ClearPendingDeletes(acctEmail, folder, uids)
			logging.Info("delete", "messages deleted", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", len(uids)), logging.Dur(time.Since(start)))
		}
		return DeleteResult{
			Account: acctEmail,
			Folder:  folder,
			Err:     err,
		}
	}
}

// ExportMessages exports one or more messages to a ZIP file in ~/Downloads.
// Each message gets a folder with metadata.txt, message.txt, message.html, and attachments.
func (c *Coordinator) ExportMessages(acctEmail, folder string, messages []email.Message) tea.Cmd {
	return func() tea.Msg {
		logging.Info("export", "exporting messages", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", len(messages)))
		start := time.Now()

		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			return util.ExportResultMsg{Err: fmt.Errorf("no IMAP worker for %s", acctEmail)}
		}

		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		for i, msg := range messages {
			// Determine folder for this message (may differ in search results).
			// Folder may contain "+OtherFolder" suffixes from dedup — use only the first.
			msgFolder := folder
			if msg.Folder != "" {
				msgFolder = msg.Folder
				if idx := strings.Index(msgFolder, " +"); idx > 0 {
					msgFolder = msgFolder[:idx]
				}
			}
			msgAcct := acctEmail
			if msg.Account != "" {
				msgAcct = msg.Account
			}

			// Use the right IMAP worker for this message's account.
			mw := c.getIMAPWorker(msgAcct)
			if mw == nil {
				continue
			}

			raw, err := mw.FetchRawMessage(msgFolder, msg.UID)
			if err != nil {
				logging.Error("export", "fetch failed", logging.Acct(msgAcct), logging.MsgUID(msg.UID), logging.Err(err))
				continue
			}

			body, _ := ParseBody(raw)
			attachments, _ := ExtractAttachmentData(raw)

			dirName := sanitizeExportName(msg.Subject, msg.UID)

			// metadata.txt
			meta := fmt.Sprintf("From:    %s\nTo:      %s\nDate:    %s\nSubject: %s\nAccount: %s\nFolder:  %s\n",
				msg.From, msg.To, msg.Date.Format("2006-01-02 15:04:05 MST"), msg.Subject, msgAcct, msgFolder)
			if len(attachments) > 0 {
				meta += "\nAttachments:\n"
				for _, att := range attachments {
					meta += fmt.Sprintf("  - %s (%d bytes)\n", att.Filename, len(att.Data))
				}
			}
			writeToZip(zw, dirName+"/metadata.txt", []byte(meta))

			// message.txt
			if body.Text != "" {
				writeToZip(zw, dirName+"/message.txt", []byte(body.Text))
			}

			// message.html
			if body.HTML != "" {
				writeToZip(zw, dirName+"/message.html", []byte(body.HTML))
			}

			// attachments
			for _, att := range attachments {
				safeName := filepath.Base(att.Filename)
				if safeName == "" || safeName == "." || safeName == "/" {
					safeName = "attachment"
				}
				writeToZip(zw, dirName+"/"+safeName, att.Data)
			}

			if c.program != nil && len(messages) > 1 {
				c.program.Send(ExportProgressMsg{Done: i + 1, Total: len(messages)})
			}
		}

		if err := zw.Close(); err != nil {
			return util.ExportResultMsg{Err: fmt.Errorf("close zip: %w", err)}
		}

		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, "Downloads")
		os.MkdirAll(dir, 0755)

		zipName := fmt.Sprintf("vimail-export-%s.zip", time.Now().Format("20060102-150405"))
		zipPath := uniquePath(filepath.Join(dir, zipName))
		if err := os.WriteFile(zipPath, buf.Bytes(), 0600); err != nil {
			return util.ExportResultMsg{Err: fmt.Errorf("write zip: %w", err)}
		}

		logging.Info("export", "export complete", logging.KV("path", zipPath), logging.KV("count", len(messages)), logging.Dur(time.Since(start)))
		return util.ExportResultMsg{Path: zipPath, Count: len(messages)}
	}
}

func sanitizeExportName(subject string, uid uint32) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	safe := replacer.Replace(subject)
	safe = strings.TrimSpace(safe)
	if safe == "" {
		safe = "no-subject"
	}
	runes := []rune(safe)
	if len(runes) > 80 {
		safe = string(runes[:80])
	}
	return fmt.Sprintf("%s_%d", safe, uid)
}

func writeToZip(zw *zip.Writer, name string, data []byte) {
	w, err := zw.Create(name)
	if err != nil {
		return
	}
	w.Write(data)
}

// RestoreFromTrash queues and attempts a server-first restore. Cache rows stay
// in Trash until IMAP confirms the move, then they are reconciled using the
// destination UIDs returned by MOVE/COPYUID.
func (c *Coordinator) RestoreFromTrash(acctEmail string, uids []uint32, dstFolder string) tea.Cmd {
	return func() tea.Msg {
		logging.Info("restore", "restoring from trash", logging.Acct(acctEmail), logging.Fld(dstFolder), logging.KV("count", len(uids)))
		start := time.Now()
		if strings.EqualFold(dstFolder, "INBOX") {
			dstFolder = "Inbox"
		}
		opID, err := c.store.QueueOp(cache.OpRestore, acctEmail, "Trash", cache.RestorePayload{UIDs: uids, Destination: dstFolder})
		result := RestoreResult{Account: acctEmail, DstFolder: dstFolder, OpID: opID}
		if err != nil {
			result.Err = fmt.Errorf("queue restore: %w", err)
			return result
		}
		if !c.store.StartOp(opID) {
			return result // another process owns the queued operation
		}

		acct, ok := c.accountByEmail(acctEmail)
		if !ok {
			err = fmt.Errorf("no such account: %s", acctEmail)
			c.store.FailOp(opID, err.Error())
			result.Err = err
			return result
		}
		w, err := c.ensureIMAPWorkerForWrite(acct)
		if err != nil {
			c.store.FailOp(opID, err.Error())
			result.Err = err
			return result
		}

		result = c.executeRestore(w, acctEmail, uids, dstFolder)
		result.OpID = opID
		if result.Delivered {
			// Never retry a server-confirmed restore, even when local cache
			// reconciliation reported a warning: retrying could duplicate mail.
			c.store.CompleteOp(opID)
		} else {
			if len(result.Remaining) > 0 && result.Count > 0 {
				_ = c.store.UpdateOpPayload(opID, cache.RestorePayload{UIDs: result.Remaining, Destination: dstFolder})
			}
			if result.Err == nil {
				result.Err = fmt.Errorf("restore was not delivered")
			}
			c.store.FailOp(opID, result.Err.Error())
		}
		if result.Err != nil {
			logging.Error("restore", "restore failed", logging.Acct(acctEmail), logging.Fld(dstFolder), logging.KV("count", len(uids)), logging.Dur(time.Since(start)), logging.Err(result.Err))
		} else {
			logging.Info("restore", "messages restored", logging.Acct(acctEmail), logging.Fld(dstFolder), logging.KV("count", result.Count), logging.Dur(time.Since(start)))
		}
		return result
	}
}

func (c *Coordinator) executeRestore(w *IMAPWorker, acctEmail string, uids []uint32, dstFolder string) RestoreResult {
	result := RestoreResult{Account: acctEmail, DstFolder: dstFolder}
	moves, moveErr := w.MoveToFolderBatchWithUIDs("Trash", uids, dstFolder)
	result.Count = len(moves)
	result.Remaining = remainingRestoreUIDs(uids, moves)
	if len(moves) > 0 {
		result.Cached, result.Err = c.reconcileRestoreCache(w, acctEmail, dstFolder, moves)
		if result.Err != nil {
			// The server move is authoritative. Surface the cache warning but
			// mark delivery complete so callers never repeat the server write.
			result.Delivered = moveErr == nil && len(moves) == len(uids)
			return result
		}
	}
	if moveErr != nil {
		result.Err = moveErr
		return result
	}
	result.Delivered = len(moves) == len(uids)
	result.Cached = result.Delivered && result.Cached
	return result
}

// remainingRestoreUIDs derives retry work from confirmed source UIDs instead
// of assuming the IMAP worker always returns a successful prefix.
func remainingRestoreUIDs(requested []uint32, moves []UIDMove) []uint32 {
	moved := make(map[uint32]struct{}, len(moves))
	for _, move := range moves {
		moved[move.Source] = struct{}{}
	}
	remaining := make([]uint32, 0, len(requested)-len(moved))
	for _, uid := range requested {
		if _, ok := moved[uid]; !ok {
			remaining = append(remaining, uid)
		}
	}
	return remaining
}

func (c *Coordinator) reconcileRestoreCache(w *IMAPWorker, acctEmail, dstFolder string, moves []UIDMove) (bool, error) {
	cacheMoves := make([]cache.UIDMove, 0, len(moves))
	allMapped := true
	for _, move := range moves {
		if move.Destination == 0 {
			allMapped = false
			break
		}
		cacheMoves = append(cacheMoves, cache.UIDMove{Source: move.Source, Destination: move.Destination})
	}
	if allMapped {
		if err := c.store.RestoreMessages(acctEmail, "Trash", dstFolder, cacheMoves); err == nil {
			return true, nil
		}
	}

	// Servers without UIDPLUS cannot report destination UIDs. Remove only the
	// server-confirmed Trash rows, then rebuild destination headers so restored
	// messages below the old incremental high-water mark are still discovered.
	sources := make([]uint32, 0, len(moves))
	for _, move := range moves {
		sources = append(sources, move.Source)
	}
	if err := c.store.RemoveMessagesByUID(acctEmail, "Trash", sources); err != nil {
		return false, fmt.Errorf("server restore succeeded but source cache cleanup failed: %w", err)
	}
	if err := c.store.PurgeFolder(acctEmail, dstFolder); err != nil {
		return false, fmt.Errorf("server restore succeeded but destination cache reset failed: %w", err)
	}
	if _, err := w.SyncFolder(dstFolder); err != nil {
		return false, fmt.Errorf("server restore succeeded but destination cache refresh failed: %w", err)
	}
	return true, nil
}

// RetryPendingOps retries any pending or failed operations from the queue.
func (c *Coordinator) RetryPendingOps() tea.Cmd {
	return func() tea.Msg {
		ops := c.store.RetryableOps()
		if len(ops) == 0 {
			return nil
		}
		logging.Info("retry", "retrying pending ops", logging.KV("count", len(ops)))

		imapErrors := make(map[string]error)
		getIMAP := func(account string) (*IMAPWorker, error) {
			if err, ok := imapErrors[account]; ok {
				return nil, err
			}
			acct, ok := c.accountByEmail(account)
			if !ok {
				err := fmt.Errorf("no such account: %s", account)
				imapErrors[account] = err
				return nil, err
			}
			w, err := c.ensureIMAPWorker(acct)
			if err != nil {
				imapErrors[account] = err
				return nil, err
			}
			return w, nil
		}

		markReadBatches := make(map[string]map[string]*markReadBatch)
		for _, op := range ops {
			if !c.store.StartOp(op.ID) {
				continue // claimed by another drainer in the meantime
			}
			var err error
			switch op.Type {
			case cache.OpDelete:
				var payload cache.DeletePayload
				if e := json.Unmarshal(op.Payload, &payload); e != nil {
					c.store.FailOp(op.ID, "bad payload: "+e.Error())
					continue
				}
				w, workerErr := getIMAP(op.Account)
				if workerErr != nil {
					err = workerErr
				} else {
					err = w.MoveToTrashBatch(op.Folder, payload.UIDs, nil)
				}
				if err == nil {
					c.store.ClearPendingDeletes(op.Account, op.Folder, payload.UIDs)
				}

			case cache.OpSend:
				var payload cache.SendPayload
				if e := json.Unmarshal(op.Payload, &payload); e != nil {
					c.store.FailOp(op.ID, "bad payload: "+e.Error())
					continue
				}
				smtpW, workerErr := c.ensureSMTPWorker(op.Account)
				if workerErr != nil {
					err = workerErr
					break
				}
				_, sentMsg, sendErr := smtpW.Send(SendRequest{
					From: payload.From, To: payload.To,
					Subject: payload.Subject, Body: payload.Body,
				})
				if sendErr != nil {
					err = sendErr
				} else {
					imapW, _ := getIMAP(op.Account)
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
				if payload.All {
					w, workerErr := getIMAP(op.Account)
					if workerErr != nil {
						err = workerErr
					} else {
						err = w.MarkAllRead(op.Folder)
					}
					break
				}
				byFolder := markReadBatches[op.Account]
				if byFolder == nil {
					byFolder = make(map[string]*markReadBatch)
					markReadBatches[op.Account] = byFolder
				}
				batch := byFolder[op.Folder]
				if batch == nil {
					batch = &markReadBatch{}
					byFolder[op.Folder] = batch
				}
				batch.uids = append(batch.uids, payload.UIDs...)
				batch.opIDs = append(batch.opIDs, op.ID)
				continue

			case cache.OpRestore:
				var payload cache.RestorePayload
				if e := json.Unmarshal(op.Payload, &payload); e != nil {
					c.store.FailOp(op.ID, "bad payload: "+e.Error())
					continue
				}
				w, workerErr := getIMAP(op.Account)
				if workerErr != nil {
					err = workerErr
					break
				}
				restore := c.executeRestore(w, op.Account, payload.UIDs, payload.Destination)
				if restore.Delivered {
					if restore.Err != nil {
						logging.Warn("retry", "restore delivered with cache warning", logging.Acct(op.Account), logging.Err(restore.Err))
					}
					err = nil
				} else {
					if restore.Count > 0 && len(restore.Remaining) > 0 {
						payload.UIDs = restore.Remaining
						_ = c.store.UpdateOpPayload(op.ID, payload)
					}
					err = restore.Err
					if err == nil {
						err = fmt.Errorf("restore was not delivered")
					}
				}

			default:
				err = fmt.Errorf("unsupported queued op type %q", op.Type)
			}

			if err != nil {
				logging.Warn("retry", "op retry failed", logging.Acct(op.Account), logging.KV("op_id", op.ID), logging.KV("op_type", string(op.Type)), logging.Err(err))
				c.store.FailOp(op.ID, err.Error())
			} else {
				c.store.CompleteOp(op.ID)
			}
		}

		for account, byFolder := range markReadBatches {
			w, err := getIMAP(account)
			if err != nil {
				for _, batch := range byFolder {
					for _, id := range batch.opIDs {
						c.store.FailOp(id, err.Error())
					}
				}
				continue
			}
			retryMarkReadBatches(c.store, account, byFolder, w.MarkReadBatch)
		}
		return nil
	}
}

// markReadBatch groups the queued mark-read ops targeting one folder.
type markReadBatch struct {
	uids  []uint32
	opIDs []int64
}

// retryMarkReadBatches executes one batched mark-read per folder and settles
// each queued op according to its own folder's outcome: ops whose folder batch
// failed are marked failed (and stay retryable), ops whose batch succeeded are
// completed. `do` is the IMAP batch call, injected for testability.
func retryMarkReadBatches(store *cache.SQLiteStore, acctEmail string, batches map[string]*markReadBatch, do func(folder string, uids []uint32) error) {
	for folder, b := range batches {
		logging.Info("retry", "batched mark_read retry", logging.Acct(acctEmail), logging.Fld(folder), logging.KV("count", len(b.uids)))
		if err := do(folder, b.uids); err != nil {
			logging.Warn("retry", "batched mark_read failed", logging.Acct(acctEmail), logging.Fld(folder), logging.Err(err))
			for _, id := range b.opIDs {
				store.FailOp(id, err.Error())
			}
			continue
		}
		for _, id := range b.opIDs {
			store.CompleteOp(id)
		}
	}
}

// ensureIMAPWorker returns a healthy, connected IMAP worker for the account,
// reusing the existing connection when possible instead of tearing down every
// cycle (avoids exhausting server connection limits).
func (c *Coordinator) ensureIMAPWorker(acct config.AccountConfig) (*IMAPWorker, error) {
	return c.ensureIMAPWorkerMode(acct, true)
}

// ensureIMAPWorkerForWrite verifies the connection before a queued write.
// MCP writes are batched, so one PING per batch is cheap and prevents an idle
// connection from poisoning every later queue operation.
func (c *Coordinator) ensureIMAPWorkerForWrite(acct config.AccountConfig) (*IMAPWorker, error) {
	return c.ensureIMAPWorkerMode(acct, true)
}

func (c *Coordinator) ensureIMAPWorkerMode(acct config.AccountConfig, healthCheck bool) (*IMAPWorker, error) {
	connectMu := c.accountConnectMutex(acct.Email)
	connectMu.Lock()
	defer connectMu.Unlock()

	if err := c.ensureCredentials(acct); err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	c.mu.Lock()
	creds := c.creds[acct.Email]
	existing := c.imap[acct.Email]
	c.mu.Unlock()

	if existing != nil && (!healthCheck || existing.Ping()) {
		logging.Debug("connect", "reusing healthy IMAP connection", logging.Acct(acct.Email))
		return existing, nil
	}

	// Connection is dead or doesn't exist — disconnect old and create new.
	if existing != nil {
		c.mu.Lock()
		delete(c.imap, acct.Email)
		c.mu.Unlock()
		existing.Disconnect()
	}

	logging.Info("connect", "connecting IMAP", logging.Acct(acct.Email), logging.KV("host", acct.IMAPHost))
	connectStart := time.Now()
	w := NewIMAPWorker(acct, creds, c.store)
	if err := w.Connect(); err != nil {
		logging.Error("connect", "IMAP connect failed", logging.Acct(acct.Email), logging.Dur(time.Since(connectStart)), logging.Err(err))
		return nil, err
	}
	logging.Info("connect", "IMAP connected", logging.Acct(acct.Email), logging.Dur(time.Since(connectStart)))

	c.mu.Lock()
	c.imap[acct.Email] = w
	c.mu.Unlock()
	return w, nil
}

// ensureSMTPWorker returns an SMTP worker for the account, creating one if
// the coordinator has not synced yet (the sync path normally creates it).
func (c *Coordinator) ensureSMTPWorker(acctEmail string) (*SMTPWorker, error) {
	connectMu := c.accountConnectMutex(acctEmail)
	connectMu.Lock()
	defer connectMu.Unlock()

	if w := c.getSMTPWorker(acctEmail); w != nil {
		return w, nil
	}
	acct, ok := c.accountByEmail(acctEmail)
	if !ok {
		return nil, fmt.Errorf("no such account: %s", acctEmail)
	}
	if acct.SMTPHost == "" {
		return nil, fmt.Errorf("no SMTP host configured for %s", acctEmail)
	}
	if err := c.ensureCredentials(acct); err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	c.mu.Lock()
	creds := c.creds[acctEmail]
	c.mu.Unlock()
	if creds == nil {
		return nil, fmt.Errorf("no credentials resolved")
	}
	w := NewSMTPWorker(acct, creds)
	c.mu.Lock()
	c.smtp[acctEmail] = w
	c.mu.Unlock()
	return w, nil
}

// SendNow synchronously sends an email through the queued SendAndArchive
// path, creating the SMTP worker (and, best-effort, the IMAP worker for the
// Sent append) if needed. For callers without a bubbletea loop (the MCP
// server). The queued op keeps a failed send retryable, and op claiming
// guarantees it executes at most once across processes.
func (c *Coordinator) SendNow(acctEmail string, req SendRequest) SendResult {
	// Worker setup failure is not fatal here: SendAndArchive still queues
	// the op and fails it retryably — the same state a TUI send leaves
	// when offline.
	if _, err := c.ensureSMTPWorker(acctEmail); err != nil {
		logging.Warn("send", "SMTP worker unavailable, send will be queued for retry", logging.Acct(acctEmail), logging.Err(err))
	}
	// Without an IMAP connection the send still works — the sent message
	// just isn't appended to the Sent folder until one exists.
	if acct, ok := c.accountByEmail(acctEmail); ok && acct.IMAPHost != "" {
		if _, err := c.ensureIMAPWorker(acct); err != nil {
			logging.Warn("send", "IMAP unavailable, skipping Sent append", logging.Acct(acctEmail), logging.Err(err))
		}
	}
	msg := c.SendAndArchive(acctEmail, req)()
	res, _ := msg.(SendResult)
	return res
}

// accountByEmail finds the config entry for an account email.
func (c *Coordinator) accountByEmail(acctEmail string) (config.AccountConfig, bool) {
	for _, acct := range c.cfg.Accounts {
		if acct.Email == acctEmail {
			return acct, true
		}
	}
	return config.AccountConfig{}, false
}

// ErrSyncLocked reports that another process currently owns the account sync.
// Callers must not treat this as a successful zero-message sync.
var ErrSyncLocked = errors.New("account sync is locked by another process")

// SyncAccountNow synchronously connects (if needed), drains the account's
// queued ops, and syncs all its folders, returning the number of newly
// fetched messages. For callers without a bubbletea loop (the MCP server).
func (c *Coordinator) SyncAccountNow(acctEmail string) (int, error) {
	acct, ok := c.accountByEmail(acctEmail)
	if !ok {
		return 0, fmt.Errorf("no such account: %s", acctEmail)
	}
	return c.syncAccount(acct, false)
}

// SyncAccountFullNow synchronously rebuilds every server folder in the local
// cache. Use this explicit recovery path when incremental UID watermarks can no
// longer reconcile moved or removed messages.
func (c *Coordinator) SyncAccountFullNow(acctEmail string) (int, error) {
	acct, ok := c.accountByEmail(acctEmail)
	if !ok {
		return 0, fmt.Errorf("no such account: %s", acctEmail)
	}
	return c.syncAccount(acct, true)
}

// SyncFolderNow synchronously syncs a single folder, connecting if needed,
// and returns the number of newly fetched messages. It honors the same
// cross-process sync lock as full account syncs.
func (c *Coordinator) SyncFolderNow(acctEmail, folder string) (int, error) {
	return c.syncFolderNow(acctEmail, folder, false)
}

// SyncFolderFullNow replaces one cached folder with an authoritative snapshot
// fetched from IMAP. The remote fetch completes before existing rows are
// removed, so a network failure leaves the old cache intact.
func (c *Coordinator) SyncFolderFullNow(acctEmail, folder string) (int, error) {
	return c.syncFolderNow(acctEmail, folder, true)
}

func (c *Coordinator) syncFolderNow(acctEmail, folder string, full bool) (int, error) {
	acct, ok := c.accountByEmail(acctEmail)
	if !ok {
		return 0, fmt.Errorf("no such account: %s", acctEmail)
	}
	release, lockOK := c.store.TryAcquireSyncLock(acctEmail)
	if !lockOK {
		return 0, fmt.Errorf("%w: %s — try again shortly", ErrSyncLocked, acctEmail)
	}
	defer release()

	w, err := c.ensureIMAPWorker(acct)
	if err != nil {
		return 0, err
	}
	if full {
		return w.SyncFolderFull(folder)
	}
	return w.SyncFolder(folder)
}

// syncAccount connects and syncs all folders for a single account, returning
// the number of newly fetched messages.
func (c *Coordinator) syncAccount(acct config.AccountConfig, full bool) (int, error) {
	if acct.IMAPHost == "" {
		return 0, nil // No IMAP configured, skip.
	}

	// Cross-process advisory lock: only one process (TUI or MCP server) may
	// sync an account at a time — the incremental-sync HighestUID watermark
	// and the UIDVALIDITY purge race otherwise. Skipping is safe because the
	// lock holder is refreshing the same account.
	release, ok := c.store.TryAcquireSyncLock(acct.Email)
	if !ok {
		logging.Info("sync", "account sync locked by another process", logging.Acct(acct.Email))
		return 0, fmt.Errorf("%w: %s — try again shortly", ErrSyncLocked, acct.Email)
	}
	defer release()

	w, err := c.ensureIMAPWorker(acct)
	if err != nil {
		return 0, err
	}

	// Retry any pending operations for this account before syncing folders.
	// Batch mark_read ops by folder to avoid flooding the server.
	ops := c.store.PendingOps()
	markReadBatches := make(map[string]*markReadBatch) // folder → UIDs + op IDs
	for _, op := range ops {
		if op.Account != acct.Email {
			continue
		}
		if !c.store.StartOpNow(op.ID) {
			continue // claimed by another drainer in the meantime
		}
		switch op.Type {
		case cache.OpDelete:
			var payload cache.DeletePayload
			if err := json.Unmarshal(op.Payload, &payload); err != nil {
				c.store.FailOp(op.ID, "bad payload: "+err.Error())
				continue
			}
			logging.Info("retry", "retrying pending delete", logging.Acct(acct.Email), logging.Fld(op.Folder), logging.KV("count", len(payload.UIDs)))
			deleteWorker, workerErr := c.ensureIMAPWorker(acct)
			if workerErr != nil {
				c.store.FailOp(op.ID, workerErr.Error())
				continue
			}
			w = deleteWorker
			if err := w.MoveToTrashBatch(op.Folder, payload.UIDs, nil); err != nil {
				logging.Warn("retry", "retry delete failed", logging.Acct(op.Account), logging.Fld(op.Folder), logging.Err(err))
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
			if payload.All {
				if err := w.MarkAllRead(op.Folder); err != nil {
					logging.Warn("retry", "retry mark-all-read failed", logging.Acct(op.Account), logging.Fld(op.Folder), logging.Err(err))
					c.store.FailOp(op.ID, err.Error())
				} else {
					c.store.CompleteOp(op.ID)
				}
				continue
			}
			// Collect UIDs and op IDs by folder so each op is settled by
			// its own folder's batch outcome.
			b := markReadBatches[op.Folder]
			if b == nil {
				b = &markReadBatch{}
				markReadBatches[op.Folder] = b
			}
			b.uids = append(b.uids, payload.UIDs...)
			b.opIDs = append(b.opIDs, op.ID)
		case cache.OpRestore:
			var payload cache.RestorePayload
			if err := json.Unmarshal(op.Payload, &payload); err != nil {
				c.store.FailOp(op.ID, "bad payload: "+err.Error())
				continue
			}
			restore := c.executeRestore(w, op.Account, payload.UIDs, payload.Destination)
			if restore.Delivered {
				c.store.CompleteOp(op.ID)
				if restore.Err != nil {
					logging.Warn("retry", "restore delivered with cache warning", logging.Acct(op.Account), logging.Err(restore.Err))
				}
			} else {
				if restore.Count > 0 && len(restore.Remaining) > 0 {
					payload.UIDs = restore.Remaining
					_ = c.store.UpdateOpPayload(op.ID, payload)
				}
				if restore.Err == nil {
					restore.Err = fmt.Errorf("restore was not delivered")
				}
				c.store.FailOp(op.ID, restore.Err.Error())
			}
		default:
			// Send ops are retried separately.
			c.store.FailOp(op.ID, "skipped during sync")
		}
	}
	// Execute batched mark_read — one SELECT+STORE per folder instead of per UID.
	retryMarkReadBatches(c.store, acct.Email, markReadBatches, func(folder string, uids []uint32) error {
		markWorker, err := c.ensureIMAPWorker(acct)
		if err != nil {
			return err
		}
		w = markWorker
		return w.MarkReadBatch(folder, uids)
	})

	// List mailboxes.
	w, err = c.ensureIMAPWorker(acct)
	if err != nil {
		return 0, err
	}
	folders, err := w.ListMailboxes()
	if err != nil {
		logging.Error("sync", "list mailboxes failed", logging.Acct(acct.Email), logging.Err(err))
		return 0, fmt.Errorf("list mailboxes: %w", err)
	}
	logging.Debug("sync", "mailboxes listed", logging.Acct(acct.Email), logging.KV("folders", len(folders)))

	// Remove any previously cached folders that are now skipped (e.g. Gmail All Mail).
	for _, name := range []string{"All Mail", "Important", "[Gmail]/Všechny zprávy", "[Gmail]Všechny zprávy"} {
		if acct.ShouldSkipFolder(name) {
			c.store.DeleteFolder(acct.Email, name)
		}
	}

	// Sync each folder with progress reporting.
	// Use STATUS pre-check to skip folders with no new messages.
	synced := 0
	newTotal := 0
	var syncErrors []error
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

		if !full {
			// Quick STATUS check: skip folder if UIDNEXT hasn't changed.
			uidNext, uidValidity, err := w.FolderStatus(folder)
			if err != nil {
				logging.Warn("sync", "folder STATUS failed", logging.Acct(acct.Email), logging.Fld(folder), logging.Err(err))
				continue
			}
			storedUV, _ := c.store.GetUIDValidity(acct.Email, folder)
			highUID, _ := c.store.HighestUID(acct.Email, folder)
			if storedUV == uidValidity && highUID > 0 && uidNext <= highUID+1 {
				logging.Debug("sync", "folder skipped, no new messages", logging.Acct(acct.Email), logging.Fld(folder), logging.KV("uidnext", uidNext), logging.KV("high_uid", highUID))
				continue
			}
		}

		synced++
		folderStart := time.Now()
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
		var n int
		if full {
			n, err = w.SyncFolderFull(folder, onProgress)
		} else {
			n, err = w.SyncFolder(folder, onProgress)
		}
		if err != nil {
			logging.Warn("sync", "folder sync error", logging.Acct(acct.Email), logging.Fld(folder), logging.Dur(time.Since(folderStart)), logging.Err(err))
			if full {
				syncErrors = append(syncErrors, fmt.Errorf("%s: %w", folder, err))
			}
		} else {
			newTotal += n
			logging.Debug("sync", "folder synced", logging.Acct(acct.Email), logging.Fld(folder), logging.Dur(time.Since(folderStart)))
		}
	}
	logging.Info("sync", "account folders synced", logging.Acct(acct.Email), logging.KV("synced", synced), logging.KV("total", len(folders)))
	if len(syncErrors) > 0 {
		return newTotal, fmt.Errorf("full sync incomplete: %w", errors.Join(syncErrors...))
	}

	// Also set up SMTP worker if configured.
	if acct.SMTPHost != "" {
		c.mu.Lock()
		creds := c.creds[acct.Email]
		c.mu.Unlock()
		smtpW := NewSMTPWorker(acct, creds)
		c.mu.Lock()
		c.smtp[acct.Email] = smtpW
		c.mu.Unlock()
	}

	return newTotal, nil
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
	logging.Info("connect", "disconnecting all workers")
	c.mu.Lock()
	workers := make(map[string]*IMAPWorker, len(c.imap))
	for k, v := range c.imap {
		workers[k] = v
	}
	c.imap = make(map[string]*IMAPWorker)
	c.smtp = make(map[string]*SMTPWorker)
	c.mu.Unlock()

	for email, w := range workers {
		w.Disconnect()
		logging.Debug("connect", "IMAP disconnected", logging.Acct(email))
	}
}
