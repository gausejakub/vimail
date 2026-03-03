package cache

import (
	"database/sql"

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

CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);
`

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// WAL mode for concurrent reads.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
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

	return db, nil
}
