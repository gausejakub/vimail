package worker

import (
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-imap/v2"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
)

// draftsFolder is the display name of the server Drafts folder.
const draftsFolder = "Drafts"

// isDraftOp reports the queued op types handled by executeDraftOp.
func isDraftOp(t cache.OpType) bool {
	return t == cache.OpDraftUpload || t == cache.OpDraftDelete || t == cache.OpSetFlag
}

// executeDraftOp delivers a draft upload/delete or a flag op. Both op
// dispatchers (account sync and RetryPendingOps) call it.
func (c *Coordinator) executeDraftOp(w *IMAPWorker, op cache.QueuedOp) error {
	switch op.Type {
	case cache.OpDraftUpload:
		var p cache.DraftUploadPayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return fmt.Errorf("bad payload: %w", err)
		}
		return c.uploadDraft(w, op.Account, p.DraftID)
	case cache.OpDraftDelete:
		var p cache.DraftDeletePayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return fmt.Errorf("bad payload: %w", err)
		}
		return w.DeleteByMessageIDs(draftsFolder, p.MessageIDs)
	case cache.OpSetFlag:
		var p cache.SetFlagPayload
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return fmt.Errorf("bad payload: %w", err)
		}
		return w.AddFlag(op.Folder, p.UIDs, p.Flag, p.MessageID)
	}
	return fmt.Errorf("not a draft op: %s", op.Type)
}

// defaultFrom is the From of a draft that names no identity.
func (c *Coordinator) defaultFrom(account string) string {
	if acct, ok := c.accountByEmail(account); ok {
		return acct.SenderAddresses()[0]
	}
	return account
}

// DraftSendRequest converts a draft into the request that sends it.
func DraftSendRequest(d email.Draft, defaultFrom string) SendRequest {
	from := d.From
	if from == "" {
		from = defaultFrom
	}
	return SendRequest{
		From: from, ToList: d.To, Cc: d.Cc, Bcc: d.Bcc,
		Subject: d.Subject, Body: d.Body,
		InReplyTo: d.InReplyTo, References: d.References,
		Attachments: d.Attachments, Source: d.Source,
	}
}

// uploadDraft writes the draft's current version to the server Drafts
// folder: APPEND the new copy (flagged \Draft), link it, then remove the
// copies it replaces. A draft deleted in the meantime has its fresh copy
// removed again.
func (c *Coordinator) uploadDraft(w *IMAPWorker, account, id string) error {
	from := c.defaultFrom(account)
	newID := NewMessageID(from)
	d, version, previous, ok := c.store.BeginDraftUpload(account, id, newID)
	if !ok {
		return nil // deleted before the upload ran
	}
	opts := ComposeOptions{IncludeBcc: true, MessageID: newID, Date: d.Date}
	if src := draftSourceValue(d.Source); src != "" {
		opts.Extra = map[string]string{draftSourceHeader: src}
	}
	_, raw, err := ComposeMessage(DraftSendRequest(d, from), opts)
	if err != nil {
		return fmt.Errorf("compose draft: %w", err)
	}
	uid, err := w.AppendWithUID(draftsFolder, raw, []imap.Flag{imap.FlagDraft, imap.FlagSeen})
	if err != nil {
		return fmt.Errorf("upload draft: %w", err)
	}
	if !c.store.FinishDraftUpload(account, id, version, newID, uid) {
		return w.DeleteByMessageIDs(draftsFolder, []string{newID})
	}
	logging.Info("draft", "draft uploaded", logging.Acct(account), logging.KV("draft_id", id), logging.KV("version", version))
	if len(previous) > 0 {
		// Best effort now; the draft_delete op queued by BeginDraftUpload
		// retries if this fails.
		if err := w.DeleteByMessageIDs(draftsFolder, previous); err != nil {
			logging.Warn("draft", "removing superseded draft copy failed", logging.Acct(account), logging.Err(err))
		}
	}
	return nil
}

// reconcileDrafts aligns local drafts with the server Drafts folder after it
// was synced (syncStart is when that sync began):
//   - server drafts not linked to a local draft are imported, unless they
//     are tombstoned (deleted or superseded locally);
//   - a local draft whose server copy disappeared is dropped, unless it has
//     edits waiting to upload or was uploaded after the sync began;
//   - drafts whose server copy is behind get an upload queued.
func (c *Coordinator) reconcileDrafts(w *IMAPWorker, account string, syncStart time.Time) {
	server := c.store.FolderMessageIDs(account, draftsFolder)
	states := c.store.DraftSyncStates(account)
	linked := map[string]bool{}
	for _, st := range states {
		linked[st.ServerMessageID] = true
		linked[st.UploadMessageID] = true
	}
	tombstones := c.store.DraftTombstones(account)

	for _, st := range states {
		if st.ServerMessageID == "" || st.UploadMessageID != "" || st.Version != st.SyncedVersion {
			continue
		}
		if _, ok := server[st.ServerMessageID]; ok || !st.UploadedAt.Before(syncStart) {
			continue
		}
		logging.Info("draft", "server copy gone, dropping local draft", logging.Acct(account), logging.KV("draft_id", st.ID))
		c.store.DropDraftLocal(account, st.ID)
	}

	for msgID, uid := range server {
		if linked[msgID] || tombstones[msgID] {
			continue
		}
		raw, err := w.PeekRawMessage(draftsFolder, uid)
		if err != nil {
			logging.Warn("draft", "fetching server draft failed", logging.Acct(account), logging.MsgUID(uid), logging.Err(err))
			continue
		}
		d, err := ParseDraftMessage(raw)
		if err != nil {
			logging.Warn("draft", "parsing server draft failed", logging.Acct(account), logging.MsgUID(uid), logging.Err(err))
			continue
		}
		id, err := c.store.ImportServerDraft(account, d, msgID, uid)
		if err != nil {
			logging.Warn("draft", "importing server draft failed", logging.Acct(account), logging.Err(err))
			continue
		}
		logging.Info("draft", "imported server draft", logging.Acct(account), logging.KV("draft_id", id))
		// The same draft changed here and in another client: vimail's version
		// was uploaded, the other client's arrives as a new draft. Mark the
		// pair so the user can tell they belong together and keep one.
		d.ID = id
		if other := c.store.DraftVersionOf(account, d); other != "" {
			c.store.MarkDraftConflict(account, id, other)
			logging.Info("draft", "two versions of a draft", logging.Acct(account), logging.KV("draft_id", id), logging.KV("other", other))
		}
	}

	if n := c.store.EnsureDraftUploads(account); n > 0 {
		logging.Info("draft", "queued uploads for drafts behind the server", logging.Acct(account), logging.KV("count", n))
	}
}

// FlushDraftOpsNow runs the account's queued draft uploads/deletes and flag
// ops immediately. It returns the first delivery error; failed ops stay
// queued and retry on the next sync.
func (c *Coordinator) FlushDraftOpsNow(account string) error {
	acct, ok := c.accountByEmail(account)
	if !ok {
		return fmt.Errorf("no such account: %s", account)
	}
	if acct.IMAPHost == "" {
		return fmt.Errorf("no IMAP server configured for %s", account)
	}
	var firstErr error
	var w *IMAPWorker
	for _, op := range c.store.RetryableOps() {
		if op.Account != account || !isDraftOp(op.Type) {
			continue
		}
		if !c.store.StartOp(op.ID) {
			continue
		}
		if w == nil {
			var err error
			if w, err = c.ensureIMAPWorkerForWrite(acct); err != nil {
				c.store.FailOp(op.ID, err.Error())
				return err
			}
		}
		if err := c.executeDraftOp(w, op); err != nil {
			c.store.FailOp(op.ID, err.Error())
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.store.CompleteOp(op.ID)
	}
	return firstErr
}

// FlushDraftOps is FlushDraftOpsNow as a tea.Cmd for the TUI.
func (c *Coordinator) FlushDraftOps(account string) tea.Cmd {
	return func() tea.Msg {
		if err := c.FlushDraftOpsNow(account); err != nil {
			logging.Warn("draft", "draft sync deferred", logging.Acct(account), logging.Err(err))
		}
		return nil
	}
}

// flagSource marks the original of a sent reply (\Answered) or forward
// ($Forwarded), in the cache at once and on the server through the queue.
func (c *Coordinator) flagSource(account string, src *email.DraftSource) {
	if src == nil || src.Folder == "" || src.UID == 0 {
		return
	}
	flag := string(imap.FlagAnswered)
	if src.Kind == email.SourceForward {
		flag = "$Forwarded"
	} else {
		c.store.SetAnsweredUIDs(account, src.Folder, []uint32{src.UID}, true)
		c.store.SetAnsweredByMessageID(account, src.MessageID)
	}
	payload := cache.SetFlagPayload{UIDs: []uint32{src.UID}, Flag: flag, MessageID: src.MessageID}
	res := c.executeStateOp(cache.OpSetFlag, account, src.Folder, payload, func(w *IMAPWorker) error {
		return w.AddFlag(src.Folder, payload.UIDs, flag, src.MessageID)
	})
	if res.Err != nil {
		logging.Warn("send", "flagging original deferred", logging.Acct(account), logging.Fld(src.Folder), logging.Err(res.Err))
	}
}

// SourceAttachments fetches the attachments of a message being forwarded,
// without marking it read.
func (c *Coordinator) SourceAttachments(account, folder string, uid uint32) ([]email.DraftAttachment, error) {
	acct, ok := c.accountByEmail(account)
	if !ok {
		return nil, fmt.Errorf("no such account: %s", account)
	}
	w, err := c.ensureIMAPWorker(acct)
	if err != nil {
		return nil, err
	}
	raw, err := w.PeekRawMessage(folder, uid)
	if err != nil {
		return nil, err
	}
	parts, err := ExtractAttachmentData(raw)
	if err != nil {
		return nil, err
	}
	var out []email.DraftAttachment
	for _, p := range parts {
		out = append(out, email.DraftAttachment{
			Filename: email.SanitizeLine(p.Filename), ContentType: p.ContentType, Data: p.Data, Size: len(p.Data),
		})
	}
	return out, nil
}
