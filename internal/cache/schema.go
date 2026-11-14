package cache

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
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
	answered  BOOLEAN NOT NULL DEFAULT 0,
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
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	type        TEXT NOT NULL,
	status      TEXT NOT NULL DEFAULT 'pending',
	account     TEXT NOT NULL,
	folder      TEXT NOT NULL DEFAULT '',
	payload     TEXT NOT NULL DEFAULT '{}',
	error       TEXT NOT NULL DEFAULT '',
	owner       TEXT NOT NULL DEFAULT '',
	lease_until DATETIME,
	attempts    INTEGER NOT NULL DEFAULT 0,
	next_attempt_at DATETIME,
	created_at  DATETIME NOT NULL,
	updated_at  DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_ops_status ON pending_ops(status);

-- Cross-process advisory locks serializing per-account sync.
CREATE TABLE IF NOT EXISTS sync_locks (
	account    TEXT PRIMARY KEY,
	owner      TEXT NOT NULL,
	expires_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);
`

func Open(path string) (*sql.DB, error) {
	// Create the file owner-only before SQLite does: sql.Open is lazy, so a
	// chmod after it would miss a brand-new database and leave it (and its
	// WAL) world-readable for the whole first session.
	onDisk := path != ":memory:" && !strings.HasPrefix(path, "file:")
	if onDisk {
		if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600); err == nil {
			f.Close()
		}
		os.Chmod(path, 0600)
	}

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
	// SQLite creates the WAL/SHM files with the main file's mode; tighten
	// any left over from older versions.
	if onDisk {
		os.Chmod(path+"-wal", 0600)
		os.Chmod(path+"-shm", 0600)
	}
	// Overwrite deleted rows so removed mail does not linger in free pages.
	db.Exec("PRAGMA secure_delete=ON")
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
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
	db.Exec(`ALTER TABLE messages ADD COLUMN answered BOOLEAN NOT NULL DEFAULT 0`)

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

	// Multi-process op claiming: owner + lease columns (migration for
	// existing databases) and the cross-process sync lock table.
	db.Exec(`ALTER TABLE pending_ops ADD COLUMN owner TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE pending_ops ADD COLUMN lease_until DATETIME`)
	db.Exec(`ALTER TABLE pending_ops ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE pending_ops ADD COLUMN next_attempt_at DATETIME`)
	db.Exec(`CREATE TABLE IF NOT EXISTS sync_locks (
		account    TEXT PRIMARY KEY,
		owner      TEXT NOT NULL,
		expires_at DATETIME NOT NULL
	)`)

	// Migrate existing pending_deletes into pending_ops.
	migratePendingDeletes(db)

	migrateFullHeaders(db)

	return db, nil
}

// HeadersVersion is the header set a cached message row holds. Rows below it
// were cached before full recipient lists and threading headers were stored;
// sync refetches their headers (bodies are kept), see StaleHeaderUIDs.
const HeadersVersion = 1

// migrateFullHeaders adds full-fidelity header columns to messages, the
// fields a draft needs to round-trip (cc, bcc, identity, threading, source,
// server copy), and the draft attachment table. Each ALTER is idempotent:
// it fails harmlessly when the column already exists.
func migrateFullHeaders(db *sql.DB) {
	for _, col := range []string{
		// JSON arrays of "Name <addr>" entries, encrypted like from/to.
		`to_list TEXT NOT NULL DEFAULT ''`,
		`cc_addr TEXT NOT NULL DEFAULT ''`,
		`bcc_addr TEXT NOT NULL DEFAULT ''`,
		`reply_to TEXT NOT NULL DEFAULT ''`,
		`delivered_to TEXT NOT NULL DEFAULT ''`,
		// Message-IDs (no brackets), stored like message_id.
		`in_reply_to TEXT NOT NULL DEFAULT ''`,
		`refs TEXT NOT NULL DEFAULT ''`,
		`thread_id TEXT NOT NULL DEFAULT ''`,
		`headers_v INTEGER NOT NULL DEFAULT 0`,
		// Outcome of OpenPGP processing when the body was fetched.
		`security TEXT NOT NULL DEFAULT ''`,
	} {
		db.Exec(`ALTER TABLE messages ADD COLUMN ` + col)
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_headers_v ON messages(folder_id, headers_v)`)

	for _, col := range []string{
		`to_list TEXT NOT NULL DEFAULT ''`,
		`cc_addr TEXT NOT NULL DEFAULT ''`,
		`bcc_addr TEXT NOT NULL DEFAULT ''`,
		`in_reply_to TEXT NOT NULL DEFAULT ''`,
		`refs TEXT NOT NULL DEFAULT ''`,
		`source TEXT NOT NULL DEFAULT ''`,
		// Server copy in the Drafts folder. version counts local saves;
		// synced_version is the version the server copy holds.
		`version INTEGER NOT NULL DEFAULT 1`,
		`synced_version INTEGER NOT NULL DEFAULT 0`,
		`server_msgid TEXT NOT NULL DEFAULT ''`,
		`server_uid INTEGER NOT NULL DEFAULT 0`,
		`upload_msgid TEXT NOT NULL DEFAULT ''`,
		`uploaded_at DATETIME`,
		// Another draft that looks like a second version of this one (the
		// same draft changed in two clients before either synced).
		`conflict_with TEXT NOT NULL DEFAULT ''`,
		// The body is Markdown: sent with an HTML alternative.
		`markdown BOOLEAN NOT NULL DEFAULT 0`,
		// OpenPGP: sign and/or encrypt when the draft is sent.
		`pgp_sign BOOLEAN NOT NULL DEFAULT 0`,
		`pgp_encrypt BOOLEAN NOT NULL DEFAULT 0`,
	} {
		db.Exec(`ALTER TABLE drafts ADD COLUMN ` + col)
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS draft_attachments (
		draft_id     TEXT NOT NULL REFERENCES drafts(id) ON DELETE CASCADE,
		idx          INTEGER NOT NULL,
		filename     TEXT NOT NULL,
		content_type TEXT NOT NULL DEFAULT '',
		size         INTEGER NOT NULL DEFAULT 0,
		data         BLOB NOT NULL,
		PRIMARY KEY (draft_id, idx)
	)`)
}

// migratePendingDeletes moves rows from the old pending_deletes table into
// pending_ops. It runs inside a single transaction so two processes racing
// at startup serialize on the write lock: the loser's SELECT then sees an
// already-emptied table, making the migration idempotent instead of
// double-inserting delete ops.
func migratePendingDeletes(db *sql.DB) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback() // no-op after a successful Commit

	rows, err := tx.Query(`SELECT account, folder, uid FROM pending_deletes`)
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
		tx.Commit()
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
		tx.Exec(`INSERT INTO pending_ops (type, status, account, folder, payload, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, '', ?, ?)`,
			string(OpDelete), string(OpPending), parts[0], parts[1], string(payload), now, now)
	}

	tx.Exec(`DELETE FROM pending_deletes`)
	tx.Commit()
}

func splitNull(s string) []string {
	for i, c := range s {
		if c == 0 {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}
