package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

type MessageState struct {
	UID      uint32
	Unread   bool
	Flagged  bool
	Answered bool
}
type OrphanSweepResult struct{ Attachments, Messages, Drafts int }

// PlaceholderUIDBase starts the reserved range used for optimistic move rows
// until the server reports the real destination UIDs.
const PlaceholderUIDBase uint32 = 0xF0000000

// SQLiteStore implements email.Store backed by a SQLite database.
type SQLiteStore struct {
	db       *sql.DB
	draftSeq atomic.Int64
	encKey   []byte // AES-256 key for body encryption at rest (nil = disabled)

	// procID identifies this process instance as the owner of claimed
	// queue ops and sync locks, so two processes sharing the cache file
	// (TUI + MCP server) never execute the same op twice.
	procID  string
	lockSeq atomic.Int64 // per-acquisition token counter for sync locks
}

// NewSQLiteStore creates a new SQLiteStore from an already-opened database.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db, procID: newProcID()}
}

// SetEncryptionKey sets the AES-256 key used to encrypt email bodies at rest.
// Pass nil to disable encryption.
// HasEncryptedBodies reports whether the cache already contains values
// encrypted with a keyring key.
func (s *SQLiteStore) HasEncryptedBodies() bool {
	var n int
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE body LIKE 'enc:%' OR html_body LIKE 'enc:%' OR subject LIKE 'enc:%') OR EXISTS(SELECT 1 FROM drafts WHERE body LIKE 'enc:%')`).Scan(&n)
	return n != 0
}

// enc encrypts a header value (sender, recipients, subject, attachment name)
// for storage. Headers are encrypted like bodies so a copied cache file does
// not reveal who the user corresponds with or about what.
func (s *SQLiteStore) enc(v string) string { return encrypt(s.encKey, v) }

// decHeaders decrypts the header fields of a scanned message in place.
// Values written before header encryption existed are returned unchanged.
func (s *SQLiteStore) decHeaders(m *email.Message) {
	m.From = decrypt(s.encKey, m.From)
	m.To = decrypt(s.encKey, m.To)
	m.Subject = decrypt(s.encKey, m.Subject)
}

// EncryptLegacyRows encrypts header fields, draft fields and attachment
// names stored in plaintext by earlier versions, and returns how many rows
// changed. It is a no-op without a key and once everything is encrypted.
func (s *SQLiteStore) EncryptLegacyRows() (int, error) {
	if len(s.encKey) == 0 {
		return 0, nil
	}
	type target struct {
		table string
		cols  []string
	}
	total := 0
	for _, t := range []target{
		{"messages", []string{"from_addr", "to_addr", "subject"}},
		{"drafts", []string{"from_addr", "to_addr", "subject"}},
		{"attachments", []string{"filename"}},
	} {
		var conds []string
		for _, c := range t.cols {
			conds = append(conds, fmt.Sprintf("(%s != '' AND %s NOT LIKE 'enc:%%')", c, c))
		}
		where := strings.Join(conds, " OR ")
		for {
			// Collect a batch first: modernc.org/sqlite cannot run updates
			// while a cursor is open on the single connection.
			rows, err := s.db.Query(`SELECT rowid, ` + strings.Join(t.cols, ", ") + ` FROM ` + t.table + ` WHERE ` + where + ` LIMIT 2000`)
			if err != nil {
				return total, err
			}
			type row struct {
				id   int64
				vals []string
			}
			var batch []row
			for rows.Next() {
				r := row{vals: make([]string, len(t.cols))}
				dest := []any{&r.id}
				for i := range r.vals {
					dest = append(dest, &r.vals[i])
				}
				if err := rows.Scan(dest...); err != nil {
					rows.Close()
					return total, err
				}
				batch = append(batch, r)
			}
			rows.Close()
			if len(batch) == 0 {
				break
			}
			tx, err := s.db.Begin()
			if err != nil {
				return total, err
			}
			var sets []string
			for _, c := range t.cols {
				sets = append(sets, c+" = ?")
			}
			stmt, err := tx.Prepare(`UPDATE ` + t.table + ` SET ` + strings.Join(sets, ", ") + ` WHERE rowid = ?`)
			if err != nil {
				tx.Rollback()
				return total, err
			}
			for _, r := range batch {
				args := make([]any, 0, len(r.vals)+1)
				for _, v := range r.vals {
					if v != "" && !strings.HasPrefix(v, encPrefix) {
						v = encrypt(s.encKey, v)
					}
					args = append(args, v)
				}
				if _, err := stmt.Exec(append(args, r.id)...); err != nil {
					stmt.Close()
					tx.Rollback()
					return total, err
				}
			}
			stmt.Close()
			if err := tx.Commit(); err != nil {
				return total, err
			}
			total += len(batch)
		}
	}
	return total, nil
}

func (s *SQLiteStore) SetEncryptionKey(key []byte) {
	s.encKey = key
}

// DB returns the underlying database for use by other layers (e.g. IMAP sync).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// DataVersion returns SQLite's connection-local change counter. The value
// changes when another database connection commits, which lets the TUI notice
// cache writes made by a concurrently running MCP process.
func (s *SQLiteStore) DataVersion() (int64, error) {
	var version int64
	err := s.db.QueryRow(`PRAGMA data_version`).Scan(&version)
	return version, err
}

func (s *SQLiteStore) IntegrityCheck(full bool) error {
	pragma := "PRAGMA quick_check"
	if full {
		pragma = "PRAGMA integrity_check"
	}
	rows, err := s.db.Query(pragma)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}
		if strings.ToLower(result) != "ok" {
			return fmt.Errorf("SQLite %s failed: %s; rebuild the vimail cache", strings.TrimPrefix(pragma, "PRAGMA "), result)
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) SweepOrphans() (OrphanSweepResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return OrphanSweepResult{}, err
	}
	defer tx.Rollback()
	var out OrphanSweepResult
	queries := []struct {
		sql   string
		count *int
	}{
		{`DELETE FROM attachments WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.folder_id=attachments.folder_id AND m.uid=attachments.uid)`, &out.Attachments},
		{`DELETE FROM messages WHERE NOT EXISTS (SELECT 1 FROM folders f WHERE f.id=messages.folder_id)`, &out.Messages},
		{`DELETE FROM drafts WHERE NOT EXISTS (SELECT 1 FROM accounts a WHERE a.email=drafts.account)`, &out.Drafts},
	}
	for _, q := range queries {
		result, err := tx.Exec(q.sql)
		if err != nil {
			return out, err
		}
		n, _ := result.RowsAffected()
		*q.count = int(n)
	}
	return out, tx.Commit()
}

func (s *SQLiteStore) SampleDecrypt(limit int) error {
	if limit <= 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT body, html_body FROM messages WHERE body LIKE 'enc:%' OR html_body LIKE 'enc:%' ORDER BY RANDOM() LIMIT ?`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var body, html string
		if err := rows.Scan(&body, &html); err != nil {
			return err
		}
		if err := decryptChecked(s.encKey, body); err != nil {
			return fmt.Errorf("decrypt cached body: %w", err)
		}
		if err := decryptChecked(s.encKey, html); err != nil {
			return fmt.Errorf("decrypt cached HTML body: %w", err)
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) MessageStates(acctEmail, folder string) (map[uint32]MessageState, error) {
	rows, err := s.db.Query(`SELECT m.uid, m.unread, m.flagged, m.answered FROM messages m JOIN folders f ON f.id=m.folder_id WHERE f.account=? AND f.name=?`, acctEmail, folder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uint32]MessageState)
	for rows.Next() {
		var state MessageState
		if err := rows.Scan(&state.UID, &state.Unread, &state.Flagged, &state.Answered); err != nil {
			return nil, err
		}
		out[state.UID] = state
	}
	return out, rows.Err()
}

// PendingUIDs returns UIDs in a folder with undelivered queued writes.
// Dead-lettered ops (failed at the attempt cap) are excluded so verification
// can heal them instead of protecting them until cleanup.
func (s *SQLiteStore) PendingUIDs(acctEmail, folder string) map[uint32]bool {
	return s.PendingUIDsSince(acctEmail, folder, time.Time{})
}

// PendingUIDsSince is PendingUIDs plus UIDs touched by any op (including
// completed ones) updated at or after since. Verification passes their start
// time so a write that finishes mid-pass is not reverted from a stale snapshot.
func (s *SQLiteStore) PendingUIDsSince(acctEmail, folder string, since time.Time) map[uint32]bool {
	out := make(map[uint32]bool)
	recent, args := "", []any{string(OpPending), string(OpRunning), string(OpFailed), opMaxAttempts}
	if !since.IsZero() {
		recent = " OR updated_at >= ?"
		args = append(args, since.UTC().Add(-time.Second).Format(time.RFC3339))
	}
	args = append(args, string(OpVerify))
	ops := s.queryOps(`SELECT id, type, status, account, folder, payload, error, attempts, next_attempt_at, created_at, updated_at FROM pending_ops
		WHERE (status IN (?, ?) OR (status = ? AND attempts < ?)`+recent+`) AND type != ?
		ORDER BY created_at, id`, args...)
	for _, op := range ops {
		if op.Account != acctEmail {
			continue
		}
		var uids []uint32
		switch op.Type {
		case OpDelete:
			if op.Folder != folder {
				continue
			}
			var p DeletePayload
			_ = json.Unmarshal(op.Payload, &p)
			uids = p.UIDs
		case OpMarkRead:
			if op.Folder != folder {
				continue
			}
			var p MarkReadPayload
			_ = json.Unmarshal(op.Payload, &p)
			uids = p.UIDs
		case OpMarkUnread:
			if op.Folder != folder {
				continue
			}
			var p MarkUnreadPayload
			_ = json.Unmarshal(op.Payload, &p)
			uids = p.UIDs
		case OpFlag:
			if op.Folder != folder {
				continue
			}
			var p FlagPayload
			_ = json.Unmarshal(op.Payload, &p)
			uids = p.UIDs
		case OpMove:
			var p MovePayload
			_ = json.Unmarshal(op.Payload, &p)
			if op.Folder != folder && p.Destination != folder {
				continue
			}
			uids = p.UIDs
			if p.Destination == folder && len(p.CacheUIDs) == len(p.UIDs) {
				uids = p.CacheUIDs
			}
		case OpRestore:
			if op.Folder != folder {
				continue
			}
			var p RestorePayload
			_ = json.Unmarshal(op.Payload, &p)
			uids = p.UIDs
		case OpSetFlag:
			if op.Folder != folder {
				continue
			}
			var p SetFlagPayload
			_ = json.Unmarshal(op.Payload, &p)
			uids = p.UIDs
		}
		for _, uid := range uids {
			out[uid] = true
		}
	}
	return out
}

// SeedAccount ensures an account row exists, creating it if needed.
func (s *SQLiteStore) SeedAccount(name, acctEmail, imapHost string, imapPort int, smtpHost string, smtpPort int) error {
	_, err := s.db.Exec(`
		INSERT INTO accounts (email, name, imap_host, imap_port, smtp_host, smtp_port)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			name = excluded.name,
			imap_host = excluded.imap_host,
			imap_port = excluded.imap_port,
			smtp_host = excluded.smtp_host,
			smtp_port = excluded.smtp_port
	`, acctEmail, name, imapHost, imapPort, smtpHost, smtpPort)
	return err
}

func (s *SQLiteStore) Accounts() []email.Account {
	rows, err := s.db.Query(`SELECT email, name FROM accounts ORDER BY rowid`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var accts []email.Account
	for rows.Next() {
		var a email.Account
		if err := rows.Scan(&a.Email, &a.Name); err != nil {
			continue
		}
		accts = append(accts, a)
	}
	return accts
}

func (s *SQLiteStore) FoldersFor(acctEmail string) []email.Folder {
	rows, err := s.db.Query(`SELECT id, name FROM folders WHERE account = ? ORDER BY id`, acctEmail)
	if err != nil {
		return nil
	}

	// Collect folder IDs and names first, then close the cursor
	// before running nested queries for unread counts.
	type folderRow struct {
		id   int
		name string
	}
	var frows []folderRow
	for rows.Next() {
		var fr folderRow
		if err := rows.Scan(&fr.id, &fr.name); err != nil {
			continue
		}
		frows = append(frows, fr)
	}
	rows.Close()

	var folders []email.Folder
	for _, fr := range frows {
		f := email.Folder{Name: fr.name}
		if fr.name == "Drafts" {
			var cnt int
			s.db.QueryRow(`SELECT COUNT(*) FROM drafts WHERE account = ?`, acctEmail).Scan(&cnt)
			f.UnreadCount = cnt
		} else {
			var cnt int
			s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE folder_id = ? AND unread = 1`, fr.id).Scan(&cnt)
			f.UnreadCount = cnt
		}
		folders = append(folders, f)
	}
	return folders
}

// MessageCount returns the total number of messages in a folder.
func (s *SQLiteStore) MessageCount(acctEmail, folder string) int {
	if folder == "Drafts" {
		return s.DraftCount(acctEmail)
	}
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return 0
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE folder_id = ?`, folderID).Scan(&count)
	return count
}

// MessagesForPage returns a page of messages with offset and limit.
func (s *SQLiteStore) MessagesForPage(acctEmail, folder string, offset, limit int) []email.Message {
	if folder == "Drafts" {
		return s.draftsFor(acctEmail)
	}

	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT uid, message_id, from_addr, to_addr, subject, body, html_body, date, unread, flagged, `+extraHeaderCols+`
		FROM messages WHERE folder_id = ?
		ORDER BY julianday(date) DESC, uid DESC
		LIMIT ? OFFSET ?
	`, folderID, limit, offset)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []email.Message
	for rows.Next() {
		var m email.Message
		var dateStr string
		var unread, flagged int
		var x extraHeaders
		if err := rows.Scan(append([]any{&m.UID, &m.MessageID, &m.From, &m.To, &m.Subject, &m.Body, &m.HTMLBody, &dateStr, &unread, &flagged}, x.dest()...)...); err != nil {
			continue
		}
		s.decHeaders(&m)
		s.apply(&x, &m)
		m.Folder, m.Account = folder, acctEmail
		m.Body = decrypt(s.encKey, m.Body)
		m.HTMLBody = decrypt(s.encKey, m.HTMLBody)
		m.ID = fmt.Sprintf("%d", m.UID)
		m.Date, _ = time.Parse(time.RFC3339, dateStr)
		m.Unread = unread != 0
		m.Flagged = flagged != 0
		msgs = append(msgs, m)
	}
	rows.Close()

	s.loadAttachments(folderID, msgs)
	return msgs
}

// MessageByUID returns a single message by UID, including any cached body.
// bodyCached reports whether the body has been fetched from the server yet;
// ok reports whether the message exists in the cache at all.
func (s *SQLiteStore) MessageByUID(acctEmail, folder string, uid uint32) (msg email.Message, bodyCached, ok bool) {
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return email.Message{}, false, false
	}

	var m email.Message
	var dateStr string
	var unread, flagged, fetched int
	var x extraHeaders
	err := s.db.QueryRow(`
		SELECT uid, message_id, from_addr, to_addr, subject, body, html_body, date, unread, flagged, body_fetched, `+extraHeaderCols+`
		FROM messages WHERE folder_id = ? AND uid = ?
	`, folderID, uid).Scan(append([]any{&m.UID, &m.MessageID, &m.From, &m.To, &m.Subject, &m.Body, &m.HTMLBody, &dateStr, &unread, &flagged, &fetched}, x.dest()...)...)
	if err != nil {
		return email.Message{}, false, false
	}
	s.decHeaders(&m)
	s.apply(&x, &m)
	m.Body = decrypt(s.encKey, m.Body)
	m.HTMLBody = decrypt(s.encKey, m.HTMLBody)
	m.ID = fmt.Sprintf("%d", m.UID)
	m.Date, _ = time.Parse(time.RFC3339, dateStr)
	m.Unread = unread != 0
	m.Flagged = flagged != 0
	m.Folder = folder
	m.Account = acctEmail

	msgs := []email.Message{m}
	s.loadAttachments(folderID, msgs)
	return msgs[0], fetched != 0, true
}

// RecentMessages returns received messages in a time window across the
// requested accounts. Sent, Drafts, and Trash are excluded, and label copies
// are collapsed by Message-ID so callers see one logical message with a real
// account/folder/UID handle.
func (s *SQLiteStore) RecentMessages(accounts []string, since, until time.Time, limit int) []email.Message {
	if !since.Before(until) || limit <= 0 {
		return nil
	}

	query := `
		SELECT m.uid, f.name, f.account, m.message_id, m.from_addr, m.to_addr,
		       m.subject, m.date, m.unread, m.flagged, ` + extraHeaderCols + `
		FROM messages m
		JOIN folders f ON m.folder_id = f.id
		WHERE julianday(m.date) >= julianday(?)
		  AND julianday(m.date) < julianday(?)
		  AND lower(f.name) NOT IN ('sent', 'drafts', 'trash')`
	args := []interface{}{since.Format(time.RFC3339), until.Format(time.RFC3339)}
	if len(accounts) > 0 {
		query += ` AND f.account IN (` + strings.TrimRight(strings.Repeat("?,", len(accounts)), ",") + `)`
		for _, account := range accounts {
			args = append(args, account)
		}
	}
	// Prefer Inbox as the surviving handle for messages that also have Gmail
	// label copies. Spam remains searchable, but loses to any non-Spam copy.
	query += ` ORDER BY julianday(m.date) DESC, f.account,
		CASE lower(f.name) WHEN 'inbox' THEN 0 WHEN 'spam' THEN 2 ELSE 1 END,
		f.name, m.uid DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type dedupKey struct {
		Account   string
		MessageID string
		Folder    string
		UID       uint32
	}
	seen := make(map[dedupKey]struct{})
	msgs := make([]email.Message, 0, limit)
	for rows.Next() {
		var m email.Message
		var dateStr string
		var unread, flagged int
		var x extraHeaders
		if err := rows.Scan(append([]any{&m.UID, &m.Folder, &m.Account, &m.MessageID, &m.From, &m.To, &m.Subject, &dateStr, &unread, &flagged}, x.dest()...)...); err != nil {
			continue
		}
		s.decHeaders(&m)
		s.apply(&x, &m)
		key := dedupKey{Account: m.Account, MessageID: m.MessageID}
		if m.MessageID == "" {
			// Without a stable identity, preserving every row is safer than
			// silently collapsing distinct same-second notifications. Modern
			// synced messages normally have Message-ID, so this is a legacy
			// cache fallback rather than the common label-copy path.
			key.Folder = m.Folder
			key.UID = m.UID
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		m.ID = fmt.Sprintf("%d", m.UID)
		m.Date, _ = time.Parse(time.RFC3339, dateStr)
		m.Unread = unread != 0
		m.Flagged = flagged != 0
		msgs = append(msgs, m)
		if len(msgs) >= limit {
			break
		}
	}
	return msgs
}

func (s *SQLiteStore) MessagesFor(acctEmail, folder string) []email.Message {
	if folder == "Drafts" {
		return s.draftsFor(acctEmail)
	}

	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT uid, message_id, from_addr, to_addr, subject, body, html_body, date, unread, flagged, `+extraHeaderCols+`
		FROM messages WHERE folder_id = ?
		ORDER BY julianday(date) DESC, uid DESC
		LIMIT 500
	`, folderID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []email.Message
	for rows.Next() {
		var m email.Message
		var dateStr string
		var unread, flagged int
		var x extraHeaders
		if err := rows.Scan(append([]any{&m.UID, &m.MessageID, &m.From, &m.To, &m.Subject, &m.Body, &m.HTMLBody, &dateStr, &unread, &flagged}, x.dest()...)...); err != nil {
			continue
		}
		s.decHeaders(&m)
		s.apply(&x, &m)
		m.Folder, m.Account = folder, acctEmail
		m.Body = decrypt(s.encKey, m.Body)
		m.HTMLBody = decrypt(s.encKey, m.HTMLBody)
		m.ID = fmt.Sprintf("%d", m.UID)
		m.Date, _ = time.Parse(time.RFC3339, dateStr)
		m.Unread = unread != 0
		m.Flagged = flagged != 0
		msgs = append(msgs, m)
	}
	rows.Close()

	s.loadAttachments(folderID, msgs)
	return msgs
}

func (s *SQLiteStore) MarkRead(acctEmail, folder, id string) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return
	}
	s.db.Exec(`UPDATE messages SET unread = 0 WHERE folder_id = ? AND uid = ?`, folderID, id)
	// Also mark the same message read in other folders (Gmail labels).
	s.syncReadAcrossFolders(acctEmail, folderID, id)
}

func (s *SQLiteStore) MarkUnread(acctEmail, folder, id string) {
	uid := uint32(0)
	_, _ = fmt.Sscan(id, &uid)
	_ = s.SetUnreadUIDs(acctEmail, folder, []uint32{uid}, true)
}

func (s *SQLiteStore) SetFlagged(acctEmail, folder, id string, flagged bool) {
	uid := uint32(0)
	_, _ = fmt.Sscan(id, &uid)
	_ = s.SetFlaggedUIDs(acctEmail, folder, []uint32{uid}, flagged)
}

// SetUnreadUIDs updates read state atomically and cascades Gmail label copies.
func (s *SQLiteStore) SetUnreadUIDs(acctEmail, folder string, uids []uint32, unread bool) error {
	return s.setMessageBoolUIDs(acctEmail, folder, uids, "unread", unread)
}

// SetFlaggedUIDs updates star state atomically and cascades Gmail label copies.
func (s *SQLiteStore) SetFlaggedUIDs(acctEmail, folder string, uids []uint32, flagged bool) error {
	return s.setMessageBoolUIDs(acctEmail, folder, uids, "flagged", flagged)
}

func (s *SQLiteStore) SetAnsweredUIDs(acctEmail, folder string, uids []uint32, answered bool) error {
	return s.setMessageBoolUIDs(acctEmail, folder, uids, "answered", answered)
}

func (s *SQLiteStore) setMessageBoolUIDs(acctEmail, folder string, uids []uint32, column string, value bool) error {
	if len(uids) == 0 {
		return nil
	}
	if column != "unread" && column != "flagged" && column != "answered" {
		return fmt.Errorf("unsupported message state %q", column)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var folderID int
	if err := tx.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return err
	}
	find, err := tx.Prepare(`SELECT message_id FROM messages WHERE folder_id = ? AND uid = ?`)
	if err != nil {
		return err
	}
	defer find.Close()
	updateOne, err := tx.Prepare(`UPDATE messages SET ` + column + ` = ? WHERE folder_id = ? AND uid = ?`)
	if err != nil {
		return err
	}
	defer updateOne.Close()
	updateCopies, err := tx.Prepare(`UPDATE messages SET ` + column + ` = ? WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)`)
	if err != nil {
		return err
	}
	defer updateCopies.Close()
	for _, uid := range uids {
		var messageID string
		if err := find.QueryRow(folderID, uid).Scan(&messageID); err != nil {
			return fmt.Errorf("message %d not found in %s/%s: %w", uid, acctEmail, folder, err)
		}
		if _, err := updateOne.Exec(value, folderID, uid); err != nil {
			return err
		}
		if messageID != "" {
			if _, err := updateCopies.Exec(value, messageID, acctEmail); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// MarkReadUIDs marks a batch read in one transaction and cascades the state
// to label copies that share a Message-ID.
func (s *SQLiteStore) MarkReadUIDs(acctEmail, folder string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return fmt.Errorf("folder %q not found for %s: %w", folder, acctEmail, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	findMessageID, err := tx.Prepare(`SELECT message_id FROM messages WHERE folder_id = ? AND uid = ?`)
	if err != nil {
		return err
	}
	defer findMessageID.Close()
	markOne, err := tx.Prepare(`UPDATE messages SET unread = 0 WHERE folder_id = ? AND uid = ?`)
	if err != nil {
		return err
	}
	defer markOne.Close()
	markCopies, err := tx.Prepare(`UPDATE messages SET unread = 0 WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)`)
	if err != nil {
		return err
	}
	defer markCopies.Close()

	for _, uid := range uids {
		var messageID string
		if err := findMessageID.QueryRow(folderID, uid).Scan(&messageID); err != nil {
			return fmt.Errorf("message %d not found in %s/%s: %w", uid, acctEmail, folder, err)
		}
		if _, err := markOne.Exec(folderID, uid); err != nil {
			return err
		}
		if messageID != "" {
			if _, err := markCopies.Exec(messageID, acctEmail); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// syncReadAcrossFolders marks copies of the same message as read in other folders.
// Gmail uses labels, so the same message appears in multiple folders with different UIDs.
func (s *SQLiteStore) syncReadAcrossFolders(acctEmail string, folderID int, uid string) {
	var messageID string
	s.db.QueryRow(`SELECT message_id FROM messages WHERE folder_id = ? AND uid = ?`, folderID, uid).Scan(&messageID)
	if messageID == "" {
		return
	}
	s.db.Exec(`
		UPDATE messages SET unread = 0
		WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)
	`, messageID, acctEmail)
}

// MarkAllRead atomically marks every cached message in a folder read, cascades
// Gmail label copies, and returns the number that changed in the selected
// folder. Server delivery is handled independently by the operation queue.
func (s *SQLiteStore) MarkAllRead(acctEmail, folder string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var folderID int
	if err := tx.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return 0, fmt.Errorf("folder %q not found for %s: %w", folder, acctEmail, err)
	}
	rows, err := tx.Query(`SELECT message_id FROM messages WHERE folder_id = ? AND unread = 1 AND message_id != ''`, folderID)
	if err != nil {
		return 0, err
	}
	var messageIDs []string
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			return 0, err
		}
		messageIDs = append(messageIDs, messageID)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE folder_id = ? AND unread = 1`, folderID).Scan(&count); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE messages SET unread = 0 WHERE folder_id = ? AND unread = 1`, folderID); err != nil {
		return 0, err
	}
	for _, messageID := range messageIDs {
		if _, err := tx.Exec(`UPDATE messages SET unread = 0 WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)`, messageID, acctEmail); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// SearchMessages searches messages across all folders for an account (or all accounts if acctEmail is empty).
// Matches against subject, from, to, and body using LIKE.
func (s *SQLiteStore) SearchMessages(acctEmail, query string, limit int) []email.Message {
	if query == "" || limit <= 0 {
		return nil
	}
	// Escape LIKE wildcards in user input.
	escaped := likeEscape.Replace(query)
	pattern := "%" + escaped + "%"
	// Cc and Reply-To are stored as JSON arrays, where <, > and & are written
	// as \u escapes: match the query the way it is stored.
	jsonQuery, _ := json.Marshal(query)
	listPattern := "%" + likeEscape.Replace(strings.Trim(string(jsonQuery), `"`)) + "%"

	// When encryption is enabled, body is encrypted in the DB so we can't
	// LIKE-search it in SQL. Search header fields in SQL, then post-filter
	// body matches in Go after decryption.
	bodySearchInSQL := len(s.encKey) == 0

	querySQL := `
		SELECT m.uid, f.name, f.account, m.message_id, m.from_addr, m.to_addr, m.subject, m.body, m.date, m.unread, m.flagged, ` + extraHeaderCols + `
		FROM messages m
		JOIN folders f ON m.folder_id = f.id`
	var args []interface{}
	if acctEmail != "" {
		querySQL += ` WHERE f.account = ?`
		args = append(args, acctEmail)
		if bodySearchInSQL {
			querySQL += ` AND (m.subject LIKE ? ESCAPE '\' OR m.from_addr LIKE ? ESCAPE '\' OR m.to_addr LIKE ? ESCAPE '\' OR m.cc_addr LIKE ? ESCAPE '\' OR m.reply_to LIKE ? ESCAPE '\' OR m.body LIKE ? ESCAPE '\')`
			args = append(args, pattern, pattern, pattern, listPattern, listPattern, pattern)
		}
	} else if bodySearchInSQL {
		querySQL += ` WHERE (m.subject LIKE ? ESCAPE '\' OR m.from_addr LIKE ? ESCAPE '\' OR m.to_addr LIKE ? ESCAPE '\' OR m.cc_addr LIKE ? ESCAPE '\' OR m.reply_to LIKE ? ESCAPE '\' OR m.body LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern, pattern, listPattern, listPattern, pattern)
	}
	// Do not LIMIT candidates before deduplication: many label copies may
	// precede later unique messages. Rows are streamed and iteration stops as
	// soon as the requested number of unique matches has been collected.
	querySQL += ` ORDER BY julianday(m.date) DESC, f.name, m.uid DESC`
	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	lowerQuery := strings.ToLower(query)

	// Deduplicate label copies by Message-ID. The legacy fallback is retained
	// for old/cache-only messages that lack one, but Message-ID prevents
	// distinct same-second notifications from collapsing together.
	type dedupKey struct {
		Account   string
		MessageID string
		Subject   string
		From      string
		Date      string
	}
	seen := make(map[dedupKey]struct{})
	var msgs []email.Message
	for rows.Next() {
		var m email.Message
		var folder, account, dateStr string
		var unread, flagged int
		var x extraHeaders
		if err := rows.Scan(append([]any{&m.UID, &folder, &account, &m.MessageID, &m.From, &m.To, &m.Subject, &m.Body, &dateStr, &unread, &flagged}, x.dest()...)...); err != nil {
			continue
		}
		s.decHeaders(&m)
		s.apply(&x, &m)
		m.Body = decrypt(s.encKey, m.Body)

		// When body wasn't searched in SQL, check it here.
		if !bodySearchInSQL {
			headerMatch := strings.Contains(strings.ToLower(m.Subject), lowerQuery) ||
				strings.Contains(strings.ToLower(m.From), lowerQuery) ||
				strings.Contains(strings.ToLower(m.To), lowerQuery) ||
				containsFold(m.Cc, lowerQuery) || containsFold(m.ReplyTo, lowerQuery)
			if !headerMatch && !strings.Contains(strings.ToLower(m.Body), lowerQuery) {
				continue
			}
		}

		m.ID = fmt.Sprintf("%d", m.UID)
		m.Date, _ = time.Parse(time.RFC3339, dateStr)
		m.Unread = unread != 0
		m.Flagged = flagged != 0
		m.Account = account

		key := dedupKey{Account: account, MessageID: m.MessageID}
		if m.MessageID == "" {
			key.Subject = m.Subject
			key.From = m.From
			key.Date = dateStr
		}
		if _, ok := seen[key]; ok {
			continue
		}
		m.Folder = folder
		seen[key] = struct{}{}
		msgs = append(msgs, m)
		if len(msgs) >= limit {
			break
		}
	}
	return msgs
}

func (s *SQLiteStore) DeleteMessage(acctEmail, folder, id string) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return
	}
	// Delete the same message from all folders (Gmail labels share the same message).
	s.deleteAcrossFolders(acctEmail, folderID, id)
	// Track as pending delete so sync won't re-add from server.
	s.db.Exec(`INSERT OR IGNORE INTO pending_deletes (folder_id, uid, account, folder) VALUES (?, ?, ?, ?)`,
		folderID, id, acctEmail, folder)
}

// deleteAcrossFolders removes copies of the same message from all folders for an account.
// Also tracks cross-folder copies as pending deletes so sync won't re-add them.
func (s *SQLiteStore) deleteAcrossFolders(acctEmail string, folderID int, uid string) {
	var messageID string
	s.db.QueryRow(`SELECT message_id FROM messages WHERE folder_id = ? AND uid = ?`, folderID, uid).Scan(&messageID)

	// If we have a message_id, find and remove copies in other folders.
	if messageID != "" {
		// Collect cross-folder copies before deleting, so we can add pending_deletes.
		rows, err := s.db.Query(`
			SELECT m.uid, f.id, f.name FROM messages m
			JOIN folders f ON m.folder_id = f.id
			WHERE m.message_id = ? AND f.account = ? AND NOT (m.folder_id = ? AND m.uid = ?)
		`, messageID, acctEmail, folderID, uid)
		if err == nil {
			type copy struct {
				uid      uint32
				folderID int
				folder   string
			}
			var copies []copy
			for rows.Next() {
				var c copy
				rows.Scan(&c.uid, &c.folderID, &c.folder)
				copies = append(copies, c)
			}
			rows.Close()
			for _, c := range copies {
				s.db.Exec(`INSERT OR IGNORE INTO pending_deletes (folder_id, uid, account, folder) VALUES (?, ?, ?, ?)`,
					c.folderID, c.uid, acctEmail, c.folder)
			}
		}
		s.db.Exec(`
			DELETE FROM messages
			WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)
		`, messageID, acctEmail)
	}

	// Always delete the specific message.
	s.db.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, folderID, uid)
}

// DeleteMessageByUID removes a single message by UID from cache.
func (s *SQLiteStore) DeleteMessageByUID(acctEmail, folder string, uid uint32) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return
	}
	s.db.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, folderID, uid)
	s.db.Exec(`DELETE FROM attachments WHERE folder_id = ? AND uid = ?`, folderID, uid)
}

// UIDMove maps a message UID in a source folder to its server-assigned UID in
// a destination folder.
type UIDMove struct {
	Source      uint32
	Destination uint32
}

// RestoreMessages moves cached message rows and attachment metadata to their
// server-assigned destination UIDs in one transaction. It intentionally does
// not create deletion tombstones: the server move has already succeeded.
func (s *SQLiteStore) RestoreMessages(acctEmail, sourceFolder, destinationFolder string, moves []UIDMove) error {
	if len(moves) == 0 {
		return nil
	}
	if sourceFolder == destinationFolder {
		return fmt.Errorf("source and destination folders must differ")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sourceID, destinationID int
	if err := tx.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, sourceFolder).Scan(&sourceID); err != nil {
		return fmt.Errorf("source folder %q not found for %s: %w", sourceFolder, acctEmail, err)
	}
	if err := tx.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, destinationFolder).Scan(&destinationID); err != nil {
		return fmt.Errorf("destination folder %q not found for %s: %w", destinationFolder, acctEmail, err)
	}

	for _, move := range moves {
		if move.Source == 0 || move.Destination == 0 {
			return fmt.Errorf("restore UID mapping must be non-zero")
		}
		var messageID, from, to, subject, body, htmlBody, date string
		var bodyFetched, unread, flagged, answered, attachmentsCached int
		if err := tx.QueryRow(`
			SELECT message_id, from_addr, to_addr, subject, body, html_body,
			       body_fetched, date, unread, flagged, answered, attachments_cached
			FROM messages WHERE folder_id = ? AND uid = ?
		`, sourceID, move.Source).Scan(
			&messageID, &from, &to, &subject, &body, &htmlBody,
			&bodyFetched, &date, &unread, &flagged, &answered, &attachmentsCached,
		); err != nil {
			return fmt.Errorf("source message %d not found in %s/%s: %w", move.Source, acctEmail, sourceFolder, err)
		}

		// A previous label copy of the same logical message must not survive
		// beside the restored row in the destination folder.
		if messageID != "" {
			if _, err := tx.Exec(`DELETE FROM messages WHERE folder_id = ? AND message_id = ?`, destinationID, messageID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, destinationID, move.Destination); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO messages (
				uid, folder_id, message_id, from_addr, to_addr, subject, body,
				html_body, body_fetched, date, unread, flagged, answered, attachments_cached
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, move.Destination, destinationID, messageID, from, to, subject, body,
			htmlBody, bodyFetched, date, unread, flagged, answered, attachmentsCached); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO attachments (folder_id, uid, filename, content_type, size, part_num)
			SELECT ?, ?, filename, content_type, size, part_num
			FROM attachments WHERE folder_id = ? AND uid = ?
		`, destinationID, move.Destination, sourceID, move.Source); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, sourceID, move.Source); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM pending_deletes WHERE (folder_id = ? AND uid = ?) OR (folder_id = ? AND uid = ?)`,
			sourceID, move.Source, destinationID, move.Destination); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MoveMessagesLocal performs the optimistic cache half of a queued server move.
func (s *SQLiteStore) MoveMessagesLocal(acctEmail, sourceFolder, destinationFolder string, uids []uint32) ([]uint32, error) {
	var destinationID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account=? AND name=?`, acctEmail, destinationFolder).Scan(&destinationID); err != nil {
		return nil, err
	}
	used := make(map[uint32]bool)
	rows, err := s.db.Query(`SELECT uid FROM messages WHERE folder_id=?`, destinationID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var uid uint32
		_ = rows.Scan(&uid)
		used[uid] = true
	}
	rows.Close()
	// Optimistic rows always live in a reserved UID range that real servers
	// do not reach. That keeps HighestUID (and so incremental sync) honest and
	// makes placeholder→server UID reconciliation collision-free.
	cacheUIDs := make([]uint32, len(uids))
	moves := make([]UIDMove, len(uids))
	next := uint32(math.MaxUint32)
	for i, uid := range uids {
		for used[next] && next > PlaceholderUIDBase {
			next--
		}
		if used[next] {
			return nil, fmt.Errorf("no free placeholder UIDs in %s/%s", acctEmail, destinationFolder)
		}
		used[next] = true
		cacheUIDs[i] = next
		moves[i] = UIDMove{Source: uid, Destination: next}
		next--
	}
	if err := s.RestoreMessages(acctEmail, sourceFolder, destinationFolder, moves); err != nil {
		return nil, err
	}
	return cacheUIDs, nil
}

// ReconcileMovedUIDs replaces optimistic destination UIDs with server UIDs.
func (s *SQLiteStore) ReconcileMovedUIDs(acctEmail, destinationFolder string, moves []UIDMove) error {
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, destinationFolder).Scan(&folderID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, move := range moves {
		if move.Destination == 0 || move.Source == move.Destination {
			continue
		}
		// Incremental sync may already have fetched the server copy; the
		// optimistic row carries the cached body, so it wins.
		if _, err := tx.Exec(`DELETE FROM attachments WHERE folder_id = ? AND uid = ?`, folderID, move.Destination); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, folderID, move.Destination); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE attachments SET uid = ? WHERE folder_id = ? AND uid = ?`, move.Destination, folderID, move.Source); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE messages SET uid = ? WHERE folder_id = ? AND uid = ?`, move.Destination, folderID, move.Source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RemoveMessagesByUID removes exact cache rows without creating server-write
// tombstones. It is the fallback after a server-confirmed move whose
// destination UIDs could not be reported.
func (s *SQLiteStore) RemoveMessagesByUID(acctEmail, folder string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, uid := range uids {
		if _, err := tx.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, folderID, uid); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM pending_deletes WHERE folder_id = ? AND uid = ?`, folderID, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteMessages batch-deletes messages by ID in a single transaction.
func (s *SQLiteStore) DeleteMessages(acctEmail, folder string, ids []string) {
	if len(ids) == 0 {
		return
	}
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return
	}
	// Collect message_ids for cross-folder sync before deleting.
	var messageIDs []string
	for _, id := range ids {
		var mid string
		s.db.QueryRow(`SELECT message_id FROM messages WHERE folder_id = ? AND uid = ?`, folderID, id).Scan(&mid)
		if mid != "" {
			messageIDs = append(messageIDs, mid)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	delMsg, _ := tx.Prepare(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`)
	insPend, _ := tx.Prepare(`INSERT OR IGNORE INTO pending_deletes (folder_id, uid, account, folder) VALUES (?, ?, ?, ?)`)
	for _, id := range ids {
		delMsg.Exec(folderID, id)
		insPend.Exec(folderID, id, acctEmail, folder)
	}
	// Delete copies from other folders (Gmail labels).
	delCross, _ := tx.Prepare(`DELETE FROM messages WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)`)
	for _, mid := range messageIDs {
		delCross.Exec(mid, acctEmail)
	}
	delCross.Close()
	delMsg.Close()
	insPend.Close()
	tx.Commit()
}

// UpsertMessage inserts or updates a message in the cache.
// Skips messages that are pending deletion (deleted locally, awaiting IMAP confirm).
func (s *SQLiteStore) UpsertMessage(acctEmail, folder string, msg email.Message) error {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return fmt.Errorf("folder %q not found for %s: %w", folder, acctEmail, err)
	}

	// Skip if this UID is pending deletion.
	var pending int
	s.db.QueryRow(`SELECT 1 FROM pending_deletes WHERE folder_id = ? AND uid = ?`, folderID, msg.UID).Scan(&pending)
	if pending == 1 {
		return nil
	}

	unread := 0
	if msg.Unread {
		unread = 1
	}
	flagged := 0
	if msg.Flagged {
		flagged = 1
	}
	answered := 0
	if msg.Answered {
		answered = 1
	}
	args := []any{msg.UID, folderID, msg.MessageID, s.enc(msg.From), s.enc(toDisplay(msg)), s.enc(msg.Subject), encrypt(s.encKey, msg.Body),
		msg.Date.Format(time.RFC3339), unread, flagged, answered}
	args = append(args, s.headerArgs(msg)...)
	_, err = s.db.Exec(`
			INSERT INTO messages (uid, folder_id, message_id, from_addr, to_addr, subject, body, date, unread, flagged, answered, `+headerSetCols+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			message_id = CASE WHEN excluded.message_id != '' THEN excluded.message_id ELSE messages.message_id END,
			from_addr = excluded.from_addr,
			to_addr = excluded.to_addr,
			subject = excluded.subject,
			date = excluded.date,
			unread = excluded.unread,
				flagged = excluded.flagged,
				answered = excluded.answered,
			`+headerUpdateSet, args...)
	return err
}

// EnsureFolder creates a folder if it doesn't exist and returns its ID.
func (s *SQLiteStore) EnsureFolder(acctEmail, name string) (int, error) {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO folders (account, name) VALUES (?, ?)`, acctEmail, name)
	if err != nil {
		return 0, err
	}
	var id int
	err = s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, name).Scan(&id)
	return id, err
}

// GetUIDValidity returns the stored UIDVALIDITY for a folder.
func (s *SQLiteStore) GetUIDValidity(acctEmail, folder string) (uint32, error) {
	var val uint32
	err := s.db.QueryRow(`SELECT uidvalidity FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&val)
	return val, err
}

// SetUIDValidity updates the stored UIDVALIDITY for a folder.
func (s *SQLiteStore) SetUIDValidity(acctEmail, folder string, val uint32) error {
	_, err := s.db.Exec(`UPDATE folders SET uidvalidity = ? WHERE account = ? AND name = ?`, val, acctEmail, folder)
	return err
}

// DeleteFolder removes a folder and all its messages from the cache.
func (s *SQLiteStore) DeleteFolder(acctEmail, folder string) error {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return err
	}
	s.db.Exec(`DELETE FROM attachments WHERE folder_id = ?`, folderID)
	s.db.Exec(`DELETE FROM messages WHERE folder_id = ?`, folderID)
	s.db.Exec(`DELETE FROM pending_deletes WHERE account = ? AND folder = ?`, acctEmail, folder)
	_, err = s.db.Exec(`DELETE FROM folders WHERE id = ?`, folderID)
	return err
}

// AllUIDs returns all UIDs for a folder, ordered by date descending (matching message list order).
func (s *SQLiteStore) AllUIDs(acctEmail, folder string) []uint32 {
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT uid FROM messages WHERE folder_id = ? ORDER BY julianday(date) DESC, uid DESC`, folderID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var uids []uint32
	for rows.Next() {
		var uid uint32
		if rows.Scan(&uid) == nil {
			uids = append(uids, uid)
		}
	}
	return uids
}

// PurgeFolder deletes all messages in a folder (used when UIDVALIDITY changes).
func (s *SQLiteStore) PurgeFolder(acctEmail, folder string) error {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM messages WHERE folder_id = ?`, folderID)
	return err
}

// ReplaceFolderHeaders atomically reconciles one folder with an authoritative
// IMAP snapshot. Existing rows with the same UID keep their cached bodies and
// attachments; stale UIDs are removed, and pending local deletes stay hidden.
func (s *SQLiteStore) ReplaceFolderHeaders(acctEmail, folder string, messages []email.Message, uidValidity uint32) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var folderID int
	if err := tx.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return fmt.Errorf("folder %q not found for %s: %w", folder, acctEmail, err)
	}
	// A temporary UID table avoids SQLite's parameter-count limit for large
	// folders and makes stale-row removal part of the same transaction.
	if _, err := tx.Exec(`CREATE TEMP TABLE IF NOT EXISTS sync_snapshot_uids (uid INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sync_snapshot_uids`); err != nil {
		return err
	}
	uidStmt, err := tx.Prepare(`INSERT OR IGNORE INTO sync_snapshot_uids (uid) VALUES (?)`)
	if err != nil {
		return err
	}
	defer uidStmt.Close()
	upsertStmt, err := tx.Prepare(`
			INSERT INTO messages (uid, folder_id, message_id, from_addr, to_addr, subject, body, date, unread, flagged, answered, ` + headerSetCols + `)
			SELECT ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM pending_deletes WHERE folder_id = ? AND uid = ?
		)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			message_id = CASE WHEN excluded.message_id != '' THEN excluded.message_id ELSE messages.message_id END,
			from_addr = excluded.from_addr,
			to_addr = excluded.to_addr,
			subject = excluded.subject,
			date = excluded.date,
			unread = excluded.unread,
				flagged = excluded.flagged,
				answered = excluded.answered,
			` + headerUpdateSet)
	if err != nil {
		return err
	}
	defer upsertStmt.Close()
	for _, msg := range messages {
		if _, err := uidStmt.Exec(msg.UID); err != nil {
			return err
		}
		unread, flagged, answered := 0, 0, 0
		if msg.Unread {
			unread = 1
		}
		if msg.Flagged {
			flagged = 1
		}
		if msg.Answered {
			answered = 1
		}
		args := []any{msg.UID, folderID, msg.MessageID, s.enc(msg.From), s.enc(toDisplay(msg)), s.enc(msg.Subject),
			msg.Date.Format(time.RFC3339), unread, flagged, answered}
		args = append(args, s.headerArgs(msg)...)
		args = append(args, folderID, msg.UID)
		if _, err := upsertStmt.Exec(args...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		DELETE FROM messages
		WHERE folder_id = ? AND uid NOT IN (SELECT uid FROM sync_snapshot_uids)
	`, folderID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE folders SET uidvalidity = ? WHERE id = ?`, uidValidity, folderID); err != nil {
		return err
	}
	return tx.Commit()
}

// HighestUID returns the highest UID stored for a folder.
func (s *SQLiteStore) HighestUID(acctEmail, folder string) (uint32, error) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return 0, err
	}
	var uid uint32
	err = s.db.QueryRow(`SELECT COALESCE(MAX(uid), 0) FROM messages WHERE folder_id = ? AND uid < ?`, folderID, PlaceholderUIDBase).Scan(&uid)
	return uid, err
}

// PendingDelete represents a message that was deleted locally but not yet on the server.
type PendingDelete struct {
	Account string
	Folder  string
	UIDs    []uint32
}

// PendingDeletes returns all pending deletions grouped by account+folder.
func (s *SQLiteStore) PendingDeletes() []PendingDelete {
	rows, err := s.db.Query(`SELECT account, folder, uid FROM pending_deletes ORDER BY account, folder`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	grouped := make(map[string]*PendingDelete)
	for rows.Next() {
		var acct, folder string
		var uid uint32
		if err := rows.Scan(&acct, &folder, &uid); err != nil {
			continue
		}
		key := acct + "\x00" + folder
		if _, ok := grouped[key]; !ok {
			grouped[key] = &PendingDelete{Account: acct, Folder: folder}
		}
		grouped[key].UIDs = append(grouped[key].UIDs, uid)
	}
	var result []PendingDelete
	for _, pd := range grouped {
		result = append(result, *pd)
	}
	return result
}

// ClearPendingDeletes removes pending delete entries after IMAP confirms deletion.
func (s *SQLiteStore) ClearPendingDeletes(acctEmail, folder string, uids []uint32) {
	for _, uid := range uids {
		s.db.Exec(`DELETE FROM pending_deletes WHERE account = ? AND folder = ? AND uid = ?`, acctEmail, folder, uid)
	}
}

// loadAttachments populates attachment metadata for a slice of messages from the database.
func (s *SQLiteStore) loadAttachments(folderID int, msgs []email.Message) {
	if len(msgs) == 0 {
		return
	}
	uidIndex := make(map[uint32]int, len(msgs))
	for i, m := range msgs {
		uidIndex[m.UID] = i
	}
	attRows, err := s.db.Query(`SELECT uid, filename, content_type, size, part_num FROM attachments WHERE folder_id = ?`, folderID)
	if err != nil {
		return
	}
	for attRows.Next() {
		var uid uint32
		var att email.Attachment
		if err := attRows.Scan(&uid, &att.Filename, &att.ContentType, &att.Size, &att.PartNum); err != nil {
			continue
		}
		att.Filename = decrypt(s.encKey, att.Filename)
		if idx, ok := uidIndex[uid]; ok {
			msgs[idx].Attachments = append(msgs[idx].Attachments, att)
		}
	}
	attRows.Close()
}

// NeedsBodyRefetch returns true if a message body should be re-fetched
// (e.g. it was cached before attachment support was added).
func (s *SQLiteStore) NeedsBodyRefetch(acctEmail, folder string, uid uint32) bool {
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return false
	}
	var cached int
	s.db.QueryRow(`SELECT attachments_cached FROM messages WHERE folder_id = ? AND uid = ?`, folderID, uid).Scan(&cached)
	return cached == 0
}

// UpdateMessageBody updates the text and HTML body of a message, along with attachment metadata.
func (s *SQLiteStore) UpdateMessageBody(acctEmail, folder string, uid uint32, body, htmlBody string, attachments []email.Attachment) error {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return err
	}
	encBody := encrypt(s.encKey, body)
	encHTML := encrypt(s.encKey, htmlBody)
	_, err = s.db.Exec(`UPDATE messages SET body = ?, html_body = ?, body_fetched = 1, attachments_cached = 1 WHERE folder_id = ? AND uid = ?`, encBody, encHTML, folderID, uid)
	if err != nil {
		return err
	}

	// Replace attachment metadata.
	s.db.Exec(`DELETE FROM attachments WHERE folder_id = ? AND uid = ?`, folderID, uid)
	for _, att := range attachments {
		s.db.Exec(`INSERT INTO attachments (folder_id, uid, filename, content_type, size, part_num) VALUES (?, ?, ?, ?, ?, ?)`,
			folderID, uid, s.enc(att.Filename), att.ContentType, att.Size, att.PartNum)
	}
	return nil
}
