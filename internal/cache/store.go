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
	if err != nil {
		return err
	}

	// Ensure default folders exist.
	for _, folder := range []string{"Inbox", "Sent", "Drafts", "Trash"} {
		_, err := s.db.Exec(`
			INSERT OR IGNORE INTO folders (account, name) VALUES (?, ?)
		`, acctEmail, folder)
		if err != nil {
			return err
		}
	}
	return nil
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
	if folder == "Trash" {
		// Already in Trash — permanently delete.
		s.db.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, folderID, id)
		return
	}
	// Move to Trash folder.
	var trashID int
	err = s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, "Trash").Scan(&trashID)
	if err != nil {
		// No Trash folder — just delete.
		s.db.Exec(`DELETE FROM messages WHERE folder_id = ? AND uid = ?`, folderID, id)
		return
	}
	s.db.Exec(`UPDATE messages SET folder_id = ? WHERE folder_id = ? AND uid = ?`, trashID, folderID, id)
}

// UpsertMessage inserts or updates a message in the cache.
func (s *SQLiteStore) UpsertMessage(acctEmail, folder string, msg email.Message) error {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return fmt.Errorf("folder %q not found for %s: %w", folder, acctEmail, err)
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
			body = excluded.body,
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

// UpdateMessageBody updates the text and HTML body of a message.
func (s *SQLiteStore) UpdateMessageBody(acctEmail, folder string, uid uint32, body, htmlBody string) error {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE messages SET body = ?, html_body = ?, body_fetched = 1 WHERE folder_id = ? AND uid = ?`, body, htmlBody, folderID, uid)
	return err
}
