package cache

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/gausejakub/vimail/internal/email"
)

// ddlV016 is the cache schema of vimail v0.16.0, verbatim, before full
// header columns and server-synced drafts existed.
const ddlV016 = `
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

// A cache written by v0.16.0 must open, keep its rows readable, report the
// rows whose headers need a refetch, and upgrade legacy drafts and encoded
// attachment names.
func TestMigrationFromV016Cache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		ddlV016,
		`INSERT INTO accounts (email, name) VALUES ('me@x.test', 'Me')`,
		`INSERT INTO folders (id, account, name, uidvalidity) VALUES (1, 'me@x.test', 'Inbox', 7)`,
		`INSERT INTO messages (uid, folder_id, message_id, from_addr, to_addr, subject, body, body_fetched, date, unread, flagged, answered)
			VALUES (5, 1, 'm5@x.test', 'Petr <p@x.test>', 'me@x.test', 'old subject', 'old body', 1, '2026-11-01T10:00:00Z', 0, 1, 1)`,
		`INSERT INTO attachments (folder_id, uid, filename, content_type, size, part_num)
			VALUES (1, 5, '=?UTF-8?Q?Digifest_2026_program_=C5=99.pdf?=', 'application/pdf', 10, '2')`,
		`INSERT INTO drafts (id, account, from_addr, to_addr, subject, body, date)
			VALUES ('draft-1', 'me@x.test', 'me@x.test', 'a@x.test, Bob <b@x.test>', 'legacy draft', 'legacy body', '2026-11-01T10:00:00Z')`,
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatalf("seed v0.16.0 cache: %v\n%s", err, stmt)
		}
	}
	old.Close()

	for pass := 0; pass < 2; pass++ { // the migration is idempotent
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open migrated cache (pass %d): %v", pass, err)
		}
		db.Close()
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewSQLiteStore(db)

	m, cached, ok := s.MessageByUID("me@x.test", "Inbox", 5)
	if !ok || !cached || m.Body != "old body" || m.Subject != "old subject" || !m.Flagged || !m.Answered ||
		len(m.ToList) != 1 || m.ToList[0] != "me@x.test" || m.ThreadID != "m5@x.test" {
		t.Fatalf("legacy row after migration = %+v (cached=%v ok=%v)", m, cached, ok)
	}
	if got := s.StaleHeaderUIDs("me@x.test", "Inbox", 10); len(got) != 1 || got[0] != 5 {
		t.Fatalf("stale header rows = %v, want [5]", got)
	}

	// Backfill (what sync does with refetched headers) fills the new fields
	// and keeps body, flags and attachments.
	if err := s.UpdateHeaders("me@x.test", "Inbox", email.Message{
		UID: 5, MessageID: "m5@x.test", From: "Petr <p@x.test>",
		ToList: []string{"me@x.test", "eva@x.test"}, Cc: []string{"senk@x.test"}, ReplyTo: []string{"list@x.test"},
		InReplyTo: "m4@x.test", References: []string{"m3@x.test", "m4@x.test"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.StaleHeaderUIDs("me@x.test", "Inbox", 10); len(got) != 0 {
		t.Fatalf("still stale after backfill: %v", got)
	}
	m, _, _ = s.MessageByUID("me@x.test", "Inbox", 5)
	if len(m.ToList) != 2 || m.Cc[0] != "senk@x.test" || m.ReplyTo[0] != "list@x.test" || m.InReplyTo != "m4@x.test" ||
		m.ThreadID != "m3@x.test" || m.Body != "old body" || !m.Flagged || len(m.Attachments) != 1 {
		t.Fatalf("after backfill = %+v", m)
	}

	if n, err := s.DecodeAttachmentNames(); err != nil || n != 1 {
		t.Fatalf("DecodeAttachmentNames = %d, %v", n, err)
	}
	m, _, _ = s.MessageByUID("me@x.test", "Inbox", 5)
	if m.Attachments[0].Filename != "Digifest 2026 program ř.pdf" {
		t.Fatalf("attachment name = %q", m.Attachments[0].Filename)
	}

	d, ok := s.Draft("me@x.test", "draft-1")
	if !ok || len(d.To) != 2 || d.To[1] != "Bob <b@x.test>" || d.Subject != "legacy draft" || d.Body != "legacy body" || d.Synced {
		t.Fatalf("legacy draft = %+v", d)
	}
	if n := s.EnsureDraftUploads("me@x.test"); n != 1 {
		t.Fatalf("legacy draft upload queued %d times, want 1", n)
	}
	if n := s.EnsureDraftUploads("me@x.test"); n != 0 {
		t.Fatalf("upload queued again: %d", n)
	}
}

// Saving a draft stores every field, bumps its version and queues exactly
// one upload while one is waiting.
func TestDraftFieldsAndUploadQueue(t *testing.T) {
	s := testStore(t)
	seedWithFolders(t, s, "Me", "me@x.test")
	d := email.Draft{
		ID: "d1", From: "Me <alias@x.test>", To: []string{"a@x.test", "B <b@x.test>"}, Cc: []string{"c@x.test"}, Bcc: []string{"d@x.test"},
		Subject: "s", Body: "b", InReplyTo: "o@x.test", References: []string{"r@x.test", "o@x.test"},
		Source:      &email.DraftSource{Kind: email.SourceReply, Folder: "Inbox", UID: 3, MessageID: "o@x.test"},
		Attachments: []email.DraftAttachment{{Filename: "f.pdf", ContentType: "application/pdf", Data: []byte("pdf")}},
	}
	for i := 0; i < 3; i++ {
		if err := s.SaveDraft("me@x.test", d); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := s.Draft("me@x.test", "d1")
	if !ok || got.From != d.From || len(got.To) != 2 || got.Cc[0] != "c@x.test" || got.Bcc[0] != "d@x.test" || got.InReplyTo != "o@x.test" ||
		len(got.References) != 2 || got.Source == nil || got.Source.UID != 3 || len(got.Attachments) != 1 || string(got.Attachments[0].Data) != "pdf" {
		t.Fatalf("draft = %+v", got)
	}
	uploads := 0
	for _, op := range s.PendingOps() {
		if op.Type == OpDraftUpload {
			uploads++
		}
	}
	if uploads != 1 {
		t.Fatalf("%d upload ops queued, want 1", uploads)
	}
	if err := s.SaveDraft("other@x.test", d); err == nil {
		t.Fatal("a draft id of another account was overwritten")
	}
	list := s.Drafts("me@x.test")
	if len(list) != 1 || list[0].Attachments[0].Data != nil || list[0].Attachments[0].Size != 3 {
		t.Fatalf("listing should carry attachment size only: %+v", list)
	}
}
