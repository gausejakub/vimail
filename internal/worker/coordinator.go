package worker

import (
	"archive/zip"
	"bytes"
	"encoding/json"
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
			logging.Error("auth", "credential resolution failed", logging.Acct(acct.Email), logging.Err(err))
			errs = append(errs, fmt.Errorf("%s: %w", acct.Email, err))
			continue
		}
		logging.Debug("auth", "credentials resolved", logging.Acct(acct.Email))
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
				ch <- syncResult{err: c.syncAccount(acct)}
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
		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			logging.Error("fetch", "no IMAP worker", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid))
			return FetchBodyResult{
				Account: acctEmail,
				Folder:  folder,
				UID:     uid,
				Err:     fmt.Errorf("no IMAP worker for %s", acctEmail),
			}
		}

		// Use dedicated fetch connection to avoid blocking behind sync.
		res, err := w.FetchBodyDirect(folder, uid)
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

// MarkRead returns a tea.Cmd that marks a message as read on the IMAP server.
// The operation is queued so it can be retried if the connection is lost.
func (c *Coordinator) MarkRead(acctEmail, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		logging.Debug("mark_read", "marking message read", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid))
		opID, _ := c.store.QueueOp(cache.OpMarkRead, acctEmail, folder, cache.MarkReadPayload{UIDs: []uint32{uid}})
		c.store.StartOp(opID)

		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			logging.Warn("mark_read", "no IMAP worker", logging.Acct(acctEmail), logging.Fld(folder), logging.MsgUID(uid))
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
		c.store.StartOp(opID)

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
		c.store.StartOp(opID)

		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			logging.Error("delete", "no IMAP worker", logging.Acct(acctEmail), logging.Fld(folder))
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

// RestoreFromTrash moves messages from Trash back to the destination folder via IMAP.
func (c *Coordinator) RestoreFromTrash(acctEmail string, uids []uint32, dstFolder string) tea.Cmd {
	return func() tea.Msg {
		logging.Info("restore", "restoring from trash", logging.Acct(acctEmail), logging.Fld(dstFolder), logging.KV("count", len(uids)))
		start := time.Now()

		w := c.getIMAPWorker(acctEmail)
		if w == nil {
			return RestoreResult{
				Account:   acctEmail,
				DstFolder: dstFolder,
				Count:     len(uids),
				Err:       fmt.Errorf("no IMAP worker for %s", acctEmail),
			}
		}

		err := w.MoveToFolderBatch("Trash", uids, dstFolder)
		if err != nil {
			logging.Error("restore", "restore failed", logging.Acct(acctEmail), logging.Fld(dstFolder), logging.KV("count", len(uids)), logging.Dur(time.Since(start)), logging.Err(err))
		} else {
			logging.Info("restore", "messages restored", logging.Acct(acctEmail), logging.Fld(dstFolder), logging.KV("count", len(uids)), logging.Dur(time.Since(start)))
		}
		return RestoreResult{
			Account:   acctEmail,
			DstFolder: dstFolder,
			Count:     len(uids),
			Err:       err,
		}
	}
}

// RetryPendingOps retries any pending or failed operations from the queue.
func (c *Coordinator) RetryPendingOps() tea.Cmd {
	return func() tea.Msg {
		ops := c.store.PendingOps()
		if len(ops) > 0 {
			logging.Info("retry", "retrying pending ops", logging.KV("count", len(ops)))
		}
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
				logging.Warn("retry", "op retry failed", logging.Acct(op.Account), logging.KV("op_id", op.ID), logging.KV("op_type", string(op.Type)), logging.Err(err))
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

	logging.Info("connect", "connecting IMAP", logging.Acct(acct.Email), logging.KV("host", acct.IMAPHost))
	connectStart := time.Now()
	w := NewIMAPWorker(acct, creds, c.store)
	if err := w.Connect(); err != nil {
		logging.Error("connect", "IMAP connect failed", logging.Acct(acct.Email), logging.Dur(time.Since(connectStart)), logging.Err(err))
		return err
	}
	logging.Info("connect", "IMAP connected", logging.Acct(acct.Email), logging.Dur(time.Since(connectStart)))

	c.mu.Lock()
	c.imap[acct.Email] = w
	c.mu.Unlock()

	// Retry any pending operations for this account before syncing folders.
	// Batch mark_read ops by folder to avoid flooding the server.
	ops := c.store.PendingOps()
	markReadByFolder := make(map[string][]uint32) // folder → UIDs
	var markReadOpIDs []int64
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
			logging.Info("retry", "retrying pending delete", logging.Acct(acct.Email), logging.Fld(op.Folder), logging.KV("count", len(payload.UIDs)))
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
			// Collect UIDs by folder for batching.
			markReadByFolder[op.Folder] = append(markReadByFolder[op.Folder], payload.UIDs...)
			markReadOpIDs = append(markReadOpIDs, op.ID)
		default:
			// Send ops are retried separately.
			c.store.FailOp(op.ID, "skipped during sync")
		}
	}
	// Execute batched mark_read — one SELECT+STORE per folder instead of per UID.
	for folder, uids := range markReadByFolder {
		logging.Info("retry", "batched mark_read retry", logging.Acct(acct.Email), logging.Fld(folder), logging.KV("count", len(uids)))
		if err := w.MarkReadBatch(folder, uids); err != nil {
			logging.Warn("retry", "batched mark_read failed", logging.Acct(acct.Email), logging.Fld(folder), logging.Err(err))
		}
	}
	for _, opID := range markReadOpIDs {
		c.store.CompleteOp(opID)
	}

	// List mailboxes.
	folders, err := w.ListMailboxes()
	if err != nil {
		logging.Error("sync", "list mailboxes failed", logging.Acct(acct.Email), logging.Err(err))
		return fmt.Errorf("list mailboxes: %w", err)
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
			logging.Warn("sync", "folder STATUS failed", logging.Acct(acct.Email), logging.Fld(folder), logging.Err(err))
			continue
		}
		storedUV, _ := c.store.GetUIDValidity(acct.Email, folder)
		highUID, _ := c.store.HighestUID(acct.Email, folder)
		if storedUV == uidValidity && highUID > 0 && uidNext <= highUID+1 {
			logging.Debug("sync", "folder skipped, no new messages", logging.Acct(acct.Email), logging.Fld(folder), logging.KV("uidnext", uidNext), logging.KV("high_uid", highUID))
			continue
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
		if _, err := w.SyncFolder(folder, onProgress); err != nil {
			logging.Warn("sync", "folder sync error", logging.Acct(acct.Email), logging.Fld(folder), logging.Dur(time.Since(folderStart)), logging.Err(err))
		} else {
			logging.Debug("sync", "folder synced", logging.Acct(acct.Email), logging.Fld(folder), logging.Dur(time.Since(folderStart)))
		}
	}
	logging.Info("sync", "account folders synced", logging.Acct(acct.Email), logging.KV("synced", synced), logging.KV("total", len(folders)))

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
	logging.Info("connect", "disconnecting all workers")
	c.mu.Lock()
	defer c.mu.Unlock()
	for email, w := range c.imap {
		w.Disconnect()
		logging.Debug("connect", "IMAP disconnected", logging.Acct(email))
	}
	c.imap = make(map[string]*IMAPWorker)
	c.smtp = make(map[string]*SMTPWorker)
}
