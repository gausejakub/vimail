package cache

import (
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

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
	err := s.db.QueryRow(`
		SELECT uid, message_id, from_addr, to_addr, subject, body, html_body, date, unread, flagged, body_fetched
		FROM messages WHERE folder_id = ? AND uid = ?
	`, folderID, uid).Scan(&m.UID, &m.MessageID, &m.From, &m.To, &m.Subject, &m.Body, &m.HTMLBody, &dateStr, &unread, &flagged, &fetched)
	if err != nil {
		return email.Message{}, false, false
	}
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
		       m.subject, m.date, m.unread, m.flagged
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
		if err := rows.Scan(&m.UID, &m.Folder, &m.Account, &m.MessageID, &m.From, &m.To, &m.Subject, &dateStr, &unread, &flagged); err != nil {
			continue
		}
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
		m.Body = decrypt(s.encKey, m.Body)
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
	`, msg.ID, acctEmail, msg.From, msg.To, msg.Subject, encrypt(s.encKey, msg.Body), msg.Date.Format(time.RFC3339))
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
	// Also mark the same message read in other folders (Gmail labels).
	s.syncReadAcrossFolders(acctEmail, folderID, id)
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

// MarkAllRead marks all messages in a folder as read.
func (s *SQLiteStore) MarkAllRead(acctEmail, folder string) {
	var folderID int
	err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID)
	if err != nil {
		return
	}
	// Collect message_ids for cross-folder sync.
	rows, err2 := s.db.Query(`SELECT message_id FROM messages WHERE folder_id = ? AND unread = 1 AND message_id != ''`, folderID)
	if err2 == nil {
		var mids []string
		for rows.Next() {
			var mid string
			rows.Scan(&mid)
			mids = append(mids, mid)
		}
		rows.Close()
		// Mark read in all folders for these message_ids.
		for _, mid := range mids {
			s.db.Exec(`UPDATE messages SET unread = 0 WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)`, mid, acctEmail)
		}
	}
	s.db.Exec(`UPDATE messages SET unread = 0 WHERE folder_id = ? AND unread = 1`, folderID)
}

// SearchMessages searches messages across all folders for an account (or all accounts if acctEmail is empty).
// Matches against subject, from, to, and body using LIKE.
func (s *SQLiteStore) SearchMessages(acctEmail, query string, limit int) []email.Message {
	if query == "" || limit <= 0 {
		return nil
	}
	// Escape LIKE wildcards in user input.
	escaped := strings.NewReplacer("%", `\%`, "_", `\_`).Replace(query)
	pattern := "%" + escaped + "%"

	// When encryption is enabled, body is encrypted in the DB so we can't
	// LIKE-search it in SQL. Search header fields in SQL, then post-filter
	// body matches in Go after decryption.
	bodySearchInSQL := len(s.encKey) == 0

	querySQL := `
		SELECT m.uid, f.name, f.account, m.message_id, m.from_addr, m.to_addr, m.subject, m.body, m.date, m.unread, m.flagged
		FROM messages m
		JOIN folders f ON m.folder_id = f.id`
	var args []interface{}
	if acctEmail != "" {
		querySQL += ` WHERE f.account = ?`
		args = append(args, acctEmail)
		if bodySearchInSQL {
			querySQL += ` AND (m.subject LIKE ? ESCAPE '\' OR m.from_addr LIKE ? ESCAPE '\' OR m.to_addr LIKE ? ESCAPE '\' OR m.body LIKE ? ESCAPE '\')`
			args = append(args, pattern, pattern, pattern, pattern)
		}
	} else if bodySearchInSQL {
		querySQL += ` WHERE (m.subject LIKE ? ESCAPE '\' OR m.from_addr LIKE ? ESCAPE '\' OR m.to_addr LIKE ? ESCAPE '\' OR m.body LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern, pattern, pattern)
	}
	// Do not LIMIT candidates before deduplication: many label copies may
	// precede later unique messages. Rows are streamed and iteration stops as
	// soon as the requested number of unique matches has been collected.
	querySQL += ` ORDER BY m.date DESC, f.name, m.uid DESC`
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
		if err := rows.Scan(&m.UID, &folder, &account, &m.MessageID, &m.From, &m.To, &m.Subject, &m.Body, &dateStr, &unread, &flagged); err != nil {
			continue
		}
		m.Body = decrypt(s.encKey, m.Body)

		// When body wasn't searched in SQL, check it here.
		if !bodySearchInSQL {
			headerMatch := strings.Contains(strings.ToLower(m.Subject), lowerQuery) ||
				strings.Contains(strings.ToLower(m.From), lowerQuery) ||
				strings.Contains(strings.ToLower(m.To), lowerQuery)
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
		var bodyFetched, unread, flagged, attachmentsCached int
		if err := tx.QueryRow(`
			SELECT message_id, from_addr, to_addr, subject, body, html_body,
			       body_fetched, date, unread, flagged, attachments_cached
			FROM messages WHERE folder_id = ? AND uid = ?
		`, sourceID, move.Source).Scan(
			&messageID, &from, &to, &subject, &body, &htmlBody,
			&bodyFetched, &date, &unread, &flagged, &attachmentsCached,
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
				html_body, body_fetched, date, unread, flagged, attachments_cached
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, move.Destination, destinationID, messageID, from, to, subject, body,
			htmlBody, bodyFetched, date, unread, flagged, attachmentsCached); err != nil {
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
	_, err = s.db.Exec(`
		INSERT INTO messages (uid, folder_id, message_id, from_addr, to_addr, subject, body, date, unread, flagged)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			message_id = CASE WHEN excluded.message_id != '' THEN excluded.message_id ELSE messages.message_id END,
			from_addr = excluded.from_addr,
			to_addr = excluded.to_addr,
			subject = excluded.subject,
			date = excluded.date,
			unread = excluded.unread,
			flagged = excluded.flagged
	`, msg.UID, folderID, msg.MessageID, msg.From, msg.To, msg.Subject, msg.Body,
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
	rows, err := s.db.Query(`SELECT uid FROM messages WHERE folder_id = ? ORDER BY date DESC`, folderID)
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
		INSERT INTO messages (uid, folder_id, message_id, from_addr, to_addr, subject, body, date, unread, flagged)
		SELECT ?, ?, ?, ?, ?, ?, '', ?, ?, ?
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
			flagged = excluded.flagged
	`)
	if err != nil {
		return err
	}
	defer upsertStmt.Close()
	for _, msg := range messages {
		if _, err := uidStmt.Exec(msg.UID); err != nil {
			return err
		}
		unread, flagged := 0, 0
		if msg.Unread {
			unread = 1
		}
		if msg.Flagged {
			flagged = 1
		}
		if _, err := upsertStmt.Exec(
			msg.UID, folderID, msg.MessageID, msg.From, msg.To, msg.Subject,
			msg.Date.Format(time.RFC3339), unread, flagged, folderID, msg.UID,
		); err != nil {
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
			folderID, uid, att.Filename, att.ContentType, att.Size, att.PartNum)
	}
	return nil
}
