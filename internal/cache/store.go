package cache

import (
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

// SQLiteStore implements email.Store backed by a SQLite database.
type SQLiteStore struct {
	db       *sql.DB
	draftSeq atomic.Int64
}

// NewSQLiteStore creates a new SQLiteStore from an already-opened database.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// DB returns the underlying database for use by other layers (e.g. IMAP sync).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
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
		SELECT uid, from_addr, to_addr, subject, body, html_body, date, unread, flagged
		FROM messages WHERE folder_id = ?
		ORDER BY date DESC
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
		if err := rows.Scan(&m.UID, &m.From, &m.To, &m.Subject, &m.Body, &m.HTMLBody, &dateStr, &unread, &flagged); err != nil {
			continue
		}
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
		SELECT uid, from_addr, to_addr, subject, body, html_body, date, unread, flagged
		FROM messages WHERE folder_id = ?
		ORDER BY date DESC
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
		if err := rows.Scan(&m.UID, &m.From, &m.To, &m.Subject, &m.Body, &m.HTMLBody, &dateStr, &unread, &flagged); err != nil {
			continue
		}
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

func (s *SQLiteStore) draftsFor(acctEmail string) []email.Message {
	rows, err := s.db.Query(`
		SELECT id, from_addr, to_addr, subject, body, date
		FROM drafts WHERE account = ?
		ORDER BY date DESC
	`, acctEmail)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []email.Message
	for rows.Next() {
		var m email.Message
		var dateStr string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Subject, &m.Body, &dateStr); err != nil {
			continue
		}
		m.Date, _ = time.Parse(time.RFC3339, dateStr)
		msgs = append(msgs, m)
	}
	return msgs
}

func (s *SQLiteStore) SaveDraft(acctEmail string, msg email.Message) {
	_, _ = s.db.Exec(`
		INSERT INTO drafts (id, account, from_addr, to_addr, subject, body, date)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			to_addr = excluded.to_addr,
			subject = excluded.subject,
			body = excluded.body,
			date = excluded.date
	`, msg.ID, acctEmail, msg.From, msg.To, msg.Subject, msg.Body, msg.Date.Format(time.RFC3339))
}

func (s *SQLiteStore) DeleteDraft(acctEmail, id string) {
	_, _ = s.db.Exec(`DELETE FROM drafts WHERE id = ? AND account = ?`, id, acctEmail)
}

func (s *SQLiteStore) NextDraftID() string {
	return fmt.Sprintf("draft-%d", s.draftSeq.Add(1))
}

func (s *SQLiteStore) MarkRead(acctEmail, folder, id string) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return
	}
	s.db.Exec(`UPDATE messages SET unread = 0 WHERE folder_id = ? AND uid = ?`, folderID, id)
}

func (s *SQLiteStore) DeleteMessage(acctEmail, folder, id string) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return
	}
	s.db.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, folderID, id)
	// Track as pending delete so sync won't re-add from server.
	s.db.Exec(`INSERT OR IGNORE INTO pending_deletes (folder_id, uid, account, folder) VALUES (?, ?, ?, ?)`,
		folderID, id, acctEmail, folder)
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
	_, err = s.db.Exec(`
		INSERT INTO messages (uid, folder_id, from_addr, to_addr, subject, body, date, unread, flagged)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			from_addr = excluded.from_addr,
			to_addr = excluded.to_addr,
			subject = excluded.subject,
			date = excluded.date,
			unread = excluded.unread,
			flagged = excluded.flagged
	`, msg.UID, folderID, msg.From, msg.To, msg.Subject, msg.Body,
		msg.Date.Format(time.RFC3339), unread, flagged)
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

// HighestUID returns the highest UID stored for a folder.
func (s *SQLiteStore) HighestUID(acctEmail, folder string) (uint32, error) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return 0, err
	}
	var uid uint32
	err = s.db.QueryRow(`SELECT COALESCE(MAX(uid), 0) FROM messages WHERE folder_id = ?`, folderID).Scan(&uid)
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
	_, err = s.db.Exec(`UPDATE messages SET body = ?, html_body = ?, body_fetched = 1, attachments_cached = 1 WHERE folder_id = ? AND uid = ?`, body, htmlBody, folderID, uid)
	if err != nil {
		return err
	}

	// Replace attachment metadata.
	s.db.Exec(`DELETE FROM attachments WHERE folder_id = ? AND uid = ?`, folderID, uid)
	for _, att := range attachments {
		s.db.Exec(`INSERT INTO attachments (folder_id, uid, filename, content_type, size, part_num) VALUES (?, ?, ?, ?, ?, ?)`,
			folderID, uid, att.Filename, att.ContentType, att.Size, att.PartNum)
	}
	return nil
}
