package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

// Drafts live in the local drafts table, which is the working copy, and in
// the account's server Drafts folder, which decides what exists:
//
//   - Every save bumps version and queues an OpDraftUpload; the upload
//     APPENDs the new copy and removes the previous one, then records
//     synced_version. A draft whose version differs from synced_version has
//     an upload pending and is never dropped by reconciliation.
//   - Deleting a draft queues an OpDraftDelete for its server copies. Those
//     ops double as tombstones, so a copy seen by a sync that started before
//     the delete is not imported again.
//   - Server drafts not linked to a local draft (written in another client)
//     are imported; a clean local draft whose server copy disappeared (sent
//     or deleted elsewhere) is dropped. See worker.reconcileDrafts.

const draftCols = `id, account, from_addr, to_addr, to_list, cc_addr, bcc_addr, subject, body, date,
	in_reply_to, refs, source, version, synced_version, server_msgid, server_uid`

func (s *SQLiteStore) scanDraft(scan func(...any) error) (email.Draft, error) {
	var d email.Draft
	var toDisp, toList, cc, bcc, refs, source, dateStr string
	var version, synced int
	err := scan(&d.ID, &d.Account, &d.From, &toDisp, &toList, &cc, &bcc, &d.Subject, &d.Body, &dateStr,
		&d.InReplyTo, &refs, &source, &version, &synced, &d.ServerMessageID, &d.ServerUID)
	if err != nil {
		return d, err
	}
	d.From = decrypt(s.encKey, d.From)
	d.Subject = decrypt(s.encKey, d.Subject)
	d.Body = decrypt(s.encKey, d.Body)
	d.To = s.decList(toList)
	if len(d.To) == 0 {
		// Drafts saved before address lists were stored kept one string.
		if disp := decrypt(s.encKey, toDisp); disp != "" {
			if list, err := email.SplitAddressField(disp); err == nil {
				d.To = list
			} else {
				d.To = []string{disp}
			}
		}
	}
	d.Cc = s.decList(cc)
	d.Bcc = s.decList(bcc)
	d.References = plainList(refs)
	if source != "" {
		var src email.DraftSource
		if json.Unmarshal([]byte(source), &src) == nil {
			d.Source = &src
		}
	}
	d.Date, _ = time.Parse(time.RFC3339, dateStr)
	d.Synced = version == synced
	return d, nil
}

// Drafts returns an account's drafts, newest first. Attachments carry name,
// type and size only; use Draft for their content.
func (s *SQLiteStore) Drafts(acctEmail string) []email.Draft {
	rows, err := s.db.Query(`SELECT `+draftCols+` FROM drafts WHERE account = ?
		ORDER BY julianday(date) DESC, id DESC`, acctEmail)
	if err != nil {
		return nil
	}
	var out []email.Draft
	for rows.Next() {
		if d, err := s.scanDraft(rows.Scan); err == nil {
			out = append(out, d)
		}
	}
	rows.Close()
	for i := range out {
		out[i].Attachments = s.draftAttachments(out[i].ID, false)
	}
	return out
}

// Draft returns one draft with attachment content.
func (s *SQLiteStore) Draft(acctEmail, id string) (email.Draft, bool) {
	row := s.db.QueryRow(`SELECT `+draftCols+` FROM drafts WHERE account = ? AND id = ?`, acctEmail, id)
	d, err := s.scanDraft(row.Scan)
	if err != nil {
		return email.Draft{}, false
	}
	d.Attachments = s.draftAttachments(id, true)
	return d, true
}

func (s *SQLiteStore) draftAttachments(id string, withData bool) []email.DraftAttachment {
	col := `NULL`
	if withData {
		col = `data`
	}
	rows, err := s.db.Query(`SELECT filename, content_type, size, `+col+` FROM draft_attachments WHERE draft_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []email.DraftAttachment
	for rows.Next() {
		var a email.DraftAttachment
		var data []byte
		if rows.Scan(&a.Filename, &a.ContentType, &a.Size, &data) != nil {
			continue
		}
		a.Filename = decrypt(s.encKey, a.Filename)
		if withData {
			a.Data = []byte(decrypt(s.encKey, string(data)))
		}
		out = append(out, a)
	}
	return out
}

// draftsFor lists drafts in the shape message lists use.
func (s *SQLiteStore) draftsFor(acctEmail string) []email.Message {
	var out []email.Message
	for _, d := range s.Drafts(acctEmail) {
		out = append(out, d.AsMessage())
	}
	return out
}

// SaveDraft creates or updates a draft and queues its upload to the server
// Drafts folder. Attachments replace the stored set and must carry content.
func (s *SQLiteStore) SaveDraft(acctEmail string, d email.Draft) error {
	for _, a := range d.Attachments {
		if a.Data == nil && a.Size > 0 {
			return fmt.Errorf("attachment %q has no content", a.Filename)
		}
	}
	if d.Date.IsZero() {
		d.Date = time.Now()
	}
	source := ""
	if d.Source != nil {
		data, _ := json.Marshal(d.Source)
		source = string(data)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	switch err := tx.QueryRow(`SELECT account FROM drafts WHERE id = ?`, d.ID).Scan(&owner); {
	case err == sql.ErrNoRows:
	case err != nil:
		return err
	case owner != acctEmail:
		return fmt.Errorf("draft %q belongs to another account", d.ID)
	}
	if _, err := tx.Exec(`
		INSERT INTO drafts (id, account, from_addr, to_addr, to_list, cc_addr, bcc_addr, subject, body, date,
			in_reply_to, refs, source, version, synced_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0)
		ON CONFLICT(id) DO UPDATE SET
			from_addr = excluded.from_addr, to_addr = excluded.to_addr, to_list = excluded.to_list,
			cc_addr = excluded.cc_addr, bcc_addr = excluded.bcc_addr,
			subject = excluded.subject, body = excluded.body, date = excluded.date,
			in_reply_to = excluded.in_reply_to, refs = excluded.refs, source = excluded.source,
			version = drafts.version + 1`,
		d.ID, acctEmail, s.enc(d.From), s.enc(email.JoinAddresses(d.To)), s.encList(d.To), s.encList(d.Cc), s.encList(d.Bcc),
		s.enc(d.Subject), encrypt(s.encKey, d.Body), d.Date.Format(time.RFC3339),
		d.InReplyTo, plainListValue(d.References), source); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM draft_attachments WHERE draft_id = ?`, d.ID); err != nil {
		return err
	}
	for i, a := range d.Attachments {
		if _, err := tx.Exec(`INSERT INTO draft_attachments (draft_id, idx, filename, content_type, size, data) VALUES (?, ?, ?, ?, ?, ?)`,
			d.ID, i, s.enc(a.Filename), a.ContentType, len(a.Data), []byte(encrypt(s.encKey, string(a.Data)))); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.queueDraftUpload(acctEmail, d.ID)
	return nil
}

// queueDraftUpload queues an upload unless one is already waiting; the
// waiting op uploads whatever version is current when it runs.
func (s *SQLiteStore) queueDraftUpload(acctEmail, id string) {
	if s.hasUnfinishedDraftUpload(acctEmail, id, true) {
		return
	}
	s.QueueOp(OpDraftUpload, acctEmail, "Drafts", DraftUploadPayload{DraftID: id})
}

// hasUnfinishedDraftUpload reports a queued or retrying upload for the draft.
// With waitingOnly, a running op does not count: it may have read the draft
// before the latest save, so a new op is needed for the new version.
func (s *SQLiteStore) hasUnfinishedDraftUpload(acctEmail, id string, waitingOnly bool) bool {
	statuses := []any{string(OpPending), string(OpFailed)}
	if !waitingOnly {
		statuses = append(statuses, string(OpRunning))
	}
	query := `SELECT payload FROM pending_ops WHERE type = ? AND account = ? AND status IN (?, ?`
	if !waitingOnly {
		query += `, ?`
	}
	query += `) AND NOT (status = ? AND attempts >= ?)`
	args := append([]any{string(OpDraftUpload), acctEmail}, statuses...)
	args = append(args, string(OpFailed), opMaxAttempts)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		var p DraftUploadPayload
		if rows.Scan(&payload) == nil && json.Unmarshal([]byte(payload), &p) == nil && p.DraftID == id {
			return true
		}
	}
	return false
}

// DeleteDraft removes a draft and queues removal of its server copies.
func (s *SQLiteStore) DeleteDraft(acctEmail, id string) {
	var serverID, uploadID string
	if err := s.db.QueryRow(`SELECT server_msgid, upload_msgid FROM drafts WHERE id = ? AND account = ?`, id, acctEmail).Scan(&serverID, &uploadID); err != nil {
		return
	}
	s.db.Exec(`DELETE FROM draft_attachments WHERE draft_id = ?`, id)
	s.db.Exec(`DELETE FROM drafts WHERE id = ? AND account = ?`, id, acctEmail)
	var ids []string
	for _, v := range []string{serverID, uploadID} {
		if v != "" {
			ids = append(ids, v)
		}
	}
	if len(ids) > 0 {
		s.QueueOp(OpDraftDelete, acctEmail, "Drafts", DraftDeletePayload{MessageIDs: ids})
	}
}

// DropDraftLocal removes a draft whose server copy is already gone.
func (s *SQLiteStore) DropDraftLocal(acctEmail, id string) {
	s.db.Exec(`DELETE FROM draft_attachments WHERE draft_id = ?`, id)
	s.db.Exec(`DELETE FROM drafts WHERE id = ? AND account = ?`, id, acctEmail)
}

// NextDraftID returns a new draft id, unique across processes and restarts.
func (s *SQLiteStore) NextDraftID() string {
	return fmt.Sprintf("draft-%d-%d", time.Now().UnixNano(), s.draftSeq.Add(1))
}

// BeginDraftUpload records the Message-ID of the copy about to be appended,
// so a concurrent Drafts sync recognizes it as this draft's and does not
// import it. It returns the draft, its version, and the Message-IDs of
// previous server copies to remove once the new copy is stored.
func (s *SQLiteStore) BeginDraftUpload(acctEmail, id, newMessageID string) (email.Draft, int, []string, bool) {
	d, ok := s.Draft(acctEmail, id)
	if !ok {
		return d, 0, nil, false
	}
	var version int
	var serverID, staleUpload string
	if err := s.db.QueryRow(`SELECT version, server_msgid, upload_msgid FROM drafts WHERE id = ? AND account = ?`, id, acctEmail).
		Scan(&version, &serverID, &staleUpload); err != nil {
		return d, 0, nil, false
	}
	var previous []string
	for _, v := range []string{serverID, staleUpload} {
		if v != "" {
			previous = append(previous, v)
		}
	}
	if len(previous) > 0 {
		// Tombstone the old copies before the new one exists: if this
		// process dies after APPEND, the retry or the sync still removes them.
		s.QueueOp(OpDraftDelete, acctEmail, "Drafts", DraftDeletePayload{MessageIDs: previous})
	}
	s.db.Exec(`UPDATE drafts SET upload_msgid = ?, server_msgid = '' WHERE id = ? AND account = ?`, newMessageID, id, acctEmail)
	return d, version, previous, true
}

// FinishDraftUpload links the stored server copy. It reports false when the
// draft was deleted during the upload; the caller then removes the copy.
func (s *SQLiteStore) FinishDraftUpload(acctEmail, id string, version int, messageID string, uid uint32) bool {
	res, err := s.db.Exec(`UPDATE drafts SET server_msgid = ?, server_uid = ?, upload_msgid = '', synced_version = ?, uploaded_at = ?
		WHERE id = ? AND account = ?`, messageID, uid, version, time.Now().UTC().Format(time.RFC3339), id, acctEmail)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// DraftSyncState is a local draft's link to its server copy.
type DraftSyncState struct {
	ID              string
	ServerMessageID string
	UploadMessageID string
	Version         int
	SyncedVersion   int
	UploadedAt      time.Time
}

// DraftSyncStates returns the server links of an account's drafts.
func (s *SQLiteStore) DraftSyncStates(acctEmail string) []DraftSyncState {
	rows, err := s.db.Query(`SELECT id, server_msgid, upload_msgid, version, synced_version, COALESCE(uploaded_at, '')
		FROM drafts WHERE account = ?`, acctEmail)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []DraftSyncState
	for rows.Next() {
		var st DraftSyncState
		var uploaded string
		if rows.Scan(&st.ID, &st.ServerMessageID, &st.UploadMessageID, &st.Version, &st.SyncedVersion, &uploaded) != nil {
			continue
		}
		st.UploadedAt, _ = time.Parse(time.RFC3339, uploaded)
		out = append(out, st)
	}
	return out
}

// DraftTombstones returns the Message-IDs of server drafts deleted or
// superseded locally (any draft_delete op still in the queue history).
func (s *SQLiteStore) DraftTombstones(acctEmail string) map[string]bool {
	out := map[string]bool{}
	rows, err := s.db.Query(`SELECT payload FROM pending_ops WHERE type = ? AND account = ?`, string(OpDraftDelete), acctEmail)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		var p DraftDeletePayload
		if rows.Scan(&payload) == nil && json.Unmarshal([]byte(payload), &p) == nil {
			for _, id := range p.MessageIDs {
				out[id] = true
			}
		}
	}
	return out
}

// ImportServerDraft stores a draft found on the server (written in another
// client) as an already-synced local draft.
func (s *SQLiteStore) ImportServerDraft(acctEmail string, d email.Draft, messageID string, uid uint32) (string, error) {
	if d.ID == "" {
		d.ID = fmt.Sprintf("draft-srv-%d-%d", time.Now().UnixNano(), s.draftSeq.Add(1))
	}
	if err := s.SaveDraft(acctEmail, d); err != nil {
		return "", err
	}
	// SaveDraft queued an upload; the server already holds this version.
	s.db.Exec(`UPDATE drafts SET synced_version = version, server_msgid = ?, server_uid = ?, uploaded_at = ?
		WHERE id = ? AND account = ?`, messageID, uid, time.Now().UTC().Format(time.RFC3339), d.ID, acctEmail)
	s.cancelDraftUploads(acctEmail, d.ID)
	return d.ID, nil
}

// cancelDraftUploads completes waiting upload ops for a draft without
// running them (the server already holds the current version).
func (s *SQLiteStore) cancelDraftUploads(acctEmail, id string) {
	rows, err := s.db.Query(`SELECT id, payload FROM pending_ops WHERE type = ? AND account = ? AND status = ?`,
		string(OpDraftUpload), acctEmail, string(OpPending))
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var opID int64
		var payload string
		var p DraftUploadPayload
		if rows.Scan(&opID, &payload) == nil && json.Unmarshal([]byte(payload), &p) == nil && p.DraftID == id {
			ids = append(ids, opID)
		}
	}
	rows.Close()
	for _, opID := range ids {
		s.db.Exec(`UPDATE pending_ops SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
			string(OpCompleted), time.Now().UTC().Format(time.RFC3339), opID, string(OpPending))
	}
}

// EnsureDraftUploads queues uploads for drafts whose server copy is behind
// and that have no upload waiting (drafts saved before server sync existed,
// or whose op was lost). It returns how many were queued.
func (s *SQLiteStore) EnsureDraftUploads(acctEmail string) int {
	n := 0
	for _, st := range s.DraftSyncStates(acctEmail) {
		if st.Version != st.SyncedVersion && !s.hasUnfinishedDraftUpload(acctEmail, st.ID, false) && !s.hasDeadDraftUpload(acctEmail, st.ID) {
			s.QueueOp(OpDraftUpload, acctEmail, "Drafts", DraftUploadPayload{DraftID: st.ID})
			n++
		}
	}
	return n
}

// hasDeadDraftUpload reports an upload that failed at the attempt cap; it is
// left for the user to see in the operation log rather than requeued forever.
func (s *SQLiteStore) hasDeadDraftUpload(acctEmail, id string) bool {
	rows, err := s.db.Query(`SELECT payload FROM pending_ops WHERE type = ? AND account = ? AND status = ? AND attempts >= ?`,
		string(OpDraftUpload), acctEmail, string(OpFailed), opMaxAttempts)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		var p DraftUploadPayload
		if rows.Scan(&payload) == nil && json.Unmarshal([]byte(payload), &p) == nil && p.DraftID == id {
			return true
		}
	}
	return false
}

// DraftCount returns the number of drafts of an account.
func (s *SQLiteStore) DraftCount(acctEmail string) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM drafts WHERE account = ?`, acctEmail).Scan(&n)
	return n
}
