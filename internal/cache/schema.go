package cache

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

const ddl = `
CREATE TABLE IF NOT EXISTS accounts (
	email    TEXT PRIMARY KEY,
	name     TEXT NOT NULL,
	imap_host TEXT NOT NULL,
	imap_port INTEGER NOT NULL DEFAULT 993,
	smtp_host TEXT NOT NULL,
	smtp_port INTEGER NOT NULL DEFAULT 587
);

CREATE TABLE IF NOT EXISTS folders (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	account  TEXT NOT NULL REFERENCES accounts(email),
	name     TEXT NOT NULL,
	uidvalidity INTEGER NOT NULL DEFAULT 0,
	UNIQUE(account, name)
);

CREATE TABLE IF NOT EXISTS messages (
	uid       INTEGER NOT NULL,
	folder_id INTEGER NOT NULL REFERENCES folders(id),
	from_addr TEXT NOT NULL,
	to_addr   TEXT NOT NULL,
	subject   TEXT NOT NULL,
	body      TEXT NOT NULL DEFAULT '',
	date      DATETIME NOT NULL,
	unread    BOOLEAN NOT NULL DEFAULT 1,
	flagged   BOOLEAN NOT NULL DEFAULT 0,
	PRIMARY KEY (folder_id, uid)
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

	return db, nil
}
