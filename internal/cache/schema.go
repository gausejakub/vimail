package cache

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "modernc.org/sqlite"
)

const ddl = `
CREATE TABLE IF NOT EXISTS accounts (
	email    TEXT PRIMARY KEY,
	name     TEXT NOT NULL,
	imap_host TEXT NOT NULL DEFAULT '',
	imap_port INTEGER NOT NULL DEFAULT 993,
	smtp_host TEXT NOT NULL DEFAULT '',
	smtp_port INTEGER NOT NULL DEFAULT 587
);

CREATE TABLE IF NOT EXISTS folders (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	account  TEXT NOT NULL REFERENCES accounts(email),
	name     TEXT NOT NULL,
	uidvalidity INTEGER NOT NULL DEFAULT 0,
	UNIQUE(account, name)
);
CREATE INDEX IF NOT EXISTS idx_folders_account ON folders(account);

CREATE TABLE IF NOT EXISTS messages (
	uid       INTEGER NOT NULL,
	folder_id INTEGER NOT NULL REFERENCES folders(id),
	message_id TEXT NOT NULL DEFAULT '',
	from_addr TEXT NOT NULL,
	to_addr   TEXT NOT NULL,
	subject   TEXT NOT NULL,
	body      TEXT NOT NULL DEFAULT '',
	html_body TEXT NOT NULL DEFAULT '',
	body_fetched BOOLEAN NOT NULL DEFAULT 0,
	date      DATETIME NOT NULL,
	unread    BOOLEAN NOT NULL DEFAULT 1,
	flagged   BOOLEAN NOT NULL DEFAULT 0,
	PRIMARY KEY (folder_id, uid)
);
CREATE INDEX IF NOT EXISTS idx_messages_folder ON messages(folder_id);

CREATE TABLE IF NOT EXISTS drafts (
	id       TEXT PRIMARY KEY,
	account  TEXT NOT NULL REFERENCES accounts(email),
	from_addr TEXT NOT NULL DEFAULT '',
	to_addr  TEXT NOT NULL DEFAULT '',
	subject  TEXT NOT NULL DEFAULT '',
	body     TEXT NOT NULL DEFAULT '',
	date     DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_drafts_account ON drafts(account);

CREATE TABLE IF NOT EXISTS pending_deletes (
	folder_id INTEGER NOT NULL REFERENCES folders(id),
	uid       INTEGER NOT NULL,
	account   TEXT NOT NULL,
	folder    TEXT NOT NULL,
	PRIMARY KEY (folder_id, uid)
);

CREATE TABLE IF NOT EXISTS attachments (
	folder_id    INTEGER NOT NULL,
	uid          INTEGER NOT NULL,
	filename     TEXT NOT NULL,
	content_type TEXT NOT NULL DEFAULT '',
	size         INTEGER NOT NULL DEFAULT 0,
	part_num     TEXT NOT NULL DEFAULT '',
	FOREIGN KEY (folder_id, uid) REFERENCES messages(folder_id, uid) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_attachments_msg ON attachments(folder_id, uid);

CREATE TABLE IF NOT EXISTS pending_ops (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	type       TEXT NOT NULL,
	status     TEXT NOT NULL DEFAULT 'pending',
	account    TEXT NOT NULL,
	folder     TEXT NOT NULL DEFAULT '',
	payload    TEXT NOT NULL DEFAULT '{}',
	error      TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_ops_status ON pending_ops(status);

CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);
`

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// Single connection avoids SQLITE_BUSY when multiple goroutines write.
	// WAL mode still allows concurrent reads within the same connection.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}

	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, err
	}

	// Add html_body column if missing (migration for existing databases).
	db.Exec(`ALTER TABLE messages ADD COLUMN html_body TEXT NOT NULL DEFAULT ''`)

	// Re-fetch bodies that were cached without HTML support (body_fetched=0
	// but body is non-empty means an older code path stored the body).
	db.Exec(`UPDATE messages SET body = '' WHERE body_fetched = 0 AND body != ''`)

	// Add attachments table if missing (migration for existing databases).
	db.Exec(`CREATE TABLE IF NOT EXISTS attachments (
		folder_id    INTEGER NOT NULL,
		uid          INTEGER NOT NULL,
		filename     TEXT NOT NULL,
		content_type TEXT NOT NULL DEFAULT '',
		size         INTEGER NOT NULL DEFAULT 0,
		part_num     TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (folder_id, uid) REFERENCES messages(folder_id, uid) ON DELETE CASCADE
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_attachments_msg ON attachments(folder_id, uid)`)

	// Track whether attachment metadata has been cached for a message.
	db.Exec(`ALTER TABLE messages ADD COLUMN attachments_cached BOOLEAN NOT NULL DEFAULT 0`)

	// Add operation queue table (migration for existing databases).
	db.Exec(`CREATE TABLE IF NOT EXISTS pending_ops (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		type       TEXT NOT NULL,
		status     TEXT NOT NULL DEFAULT 'pending',
		account    TEXT NOT NULL,
		folder     TEXT NOT NULL DEFAULT '',
		payload    TEXT NOT NULL DEFAULT '{}',
		error      TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_pending_ops_status ON pending_ops(status)`)

	// Migrate existing pending_deletes into pending_ops.
	migratePendingDeletes(db)

	return db, nil
}

// migratePendingDeletes moves rows from the old pending_deletes table into pending_ops.
func migratePendingDeletes(db *sql.DB) {
	rows, err := db.Query(`SELECT account, folder, uid FROM pending_deletes`)
	if err != nil {
		return // table may not exist
	}

	grouped := make(map[string][]uint32) // "account\x00folder" -> UIDs
	var keys []string
	for rows.Next() {
		var acct, folder string
		var uid uint32
		if err := rows.Scan(&acct, &folder, &uid); err != nil {
			continue
		}
		key := acct + "\x00" + folder
		if _, ok := grouped[key]; !ok {
			keys = append(keys, key)
		}
		grouped[key] = append(grouped[key], uid)
	}
	rows.Close()

	if len(grouped) == 0 {
		return
	}

	now := time.Now().Format(time.RFC3339)
	for _, key := range keys {
		uids := grouped[key]
		parts := splitNull(key)
		if len(parts) != 2 {
			continue
		}
		payload, _ := json.Marshal(DeletePayload{UIDs: uids})
		db.Exec(`INSERT INTO pending_ops (type, status, account, folder, payload, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, '', ?, ?)`,
			string(OpDelete), string(OpPending), parts[0], parts[1], string(payload), now, now)
	}

	db.Exec(`DELETE FROM pending_deletes`)
}

func splitNull(s string) []string {
	for i, c := range s {
		if c == 0 {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

