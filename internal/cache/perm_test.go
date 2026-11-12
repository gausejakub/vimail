package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestNewDatabaseIsOwnerOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.db")
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`INSERT INTO accounts(email,name) VALUES('a@b','a')`)
	for _, f := range []string{p, p + "-wal", p + "-shm"} {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		if st.Mode().Perm() != 0600 {
			t.Errorf("%s mode %v, want 0600", filepath.Base(f), st.Mode().Perm())
		}
	}
	var sd int
	db.QueryRow("PRAGMA secure_delete").Scan(&sd)
	if sd != 1 {
		t.Errorf("secure_delete=%d", sd)
	}
}

func TestCompletedSendDropsBodyFromQueue(t *testing.T) {
	s := testStore(t)
	id, err := s.QueueOp(OpSend, "a@example.com", "", SendPayload{From: "a@example.com", To: "b@example.com", Subject: "hi", Body: "secret text"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.StartOp(id) {
		t.Fatal("not claimed")
	}
	s.CompleteOp(id)
	var payload string
	if err := s.DB().QueryRow(`SELECT payload FROM pending_ops WHERE id = ?`, id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "secret text") || !strings.Contains(payload, "b@example.com") {
		t.Fatalf("payload after completion = %s", payload)
	}
}

func TestQueuedSendIsEncryptedAndStillReadable(t *testing.T) {
	s := testStore(t)
	key, _ := GenerateEncryptionKey()
	s.SetEncryptionKey(key)
	id, err := s.QueueOp(OpSend, "a@example.com", "", SendPayload{From: "a@example.com", To: "b@example.com", Subject: "queued subject", Body: "queued text"})
	if err != nil {
		t.Fatal(err)
	}
	raw := dumpTables(t, s, "pending_ops")
	for _, leak := range []string{"b@example.com", "queued subject", "queued text"} {
		if strings.Contains(raw, leak) {
			t.Errorf("plaintext %q in queued send", leak)
		}
	}
	ops := s.PendingOps()
	var p SendPayload
	if len(ops) != 1 || json.Unmarshal(ops[0].Payload, &p) != nil || p.Body != "queued text" || p.To != "b@example.com" {
		t.Fatalf("queued op not readable: %+v", ops)
	}
	s.StartOp(id)
	s.CompleteOp(id)
	for _, op := range s.RecentOps(10) {
		if json.Unmarshal(op.Payload, &p) != nil || p.Body != "" || p.Subject != "queued subject" {
			t.Fatalf("completed send payload = %s", op.Payload)
		}
	}
}

func TestHeadersAndAttachmentNamesAreEncryptedAtRest(t *testing.T) {
	s := testStore(t)
	const acct = "alice@example.com"
	seedWithFolders(t, s, "Alice", acct)

	// A row written before header encryption existed (no key yet).
	if err := s.UpsertMessage(acct, "Inbox", email.Message{UID: 1, From: "Legacy Sender <old@example.com>", To: acct, Subject: "legacy subject", Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	key, _ := GenerateEncryptionKey()
	s.SetEncryptionKey(key)
	if err := s.UpsertMessage(acct, "Inbox", email.Message{UID: 2, From: "Secret Sender <s@example.com>", To: acct, Subject: "secret subject", Date: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMessageBody(acct, "Inbox", 2, "secret body", "", []email.Attachment{{Filename: "secret-plan.pdf", Size: 3}}); err != nil {
		t.Fatal(err)
	}
	s.SaveDraft(acct, email.Message{ID: "d1", From: acct, To: "bob@example.com", Subject: "draft subject", Body: "draft body", Date: time.Now()})

	if n, err := s.EncryptLegacyRows(); err != nil || n != 1 {
		t.Fatalf("EncryptLegacyRows = %d, %v; want 1 legacy row", n, err)
	}
	if n, _ := s.EncryptLegacyRows(); n != 0 {
		t.Fatalf("second migration pass changed %d rows", n)
	}

	// Nothing identifying may be readable in the raw database.
	raw := dumpTables(t, s, "messages", "drafts", "attachments")
	for _, leak := range []string{"legacy subject", "old@example.com", "secret subject", "Secret Sender", "secret body", "secret-plan.pdf", "draft subject", "bob@example.com", "draft body"} {
		if strings.Contains(raw, leak) {
			t.Errorf("plaintext %q found in the cache file", leak)
		}
	}

	// And everything still reads back, searches and lists correctly.
	msgs := s.MessagesFor(acct, "Inbox")
	if len(msgs) != 2 || msgs[0].Subject != "secret subject" || msgs[1].From != "Legacy Sender <old@example.com>" {
		t.Fatalf("messages = %+v", msgs)
	}
	if len(msgs[0].Attachments) != 1 || msgs[0].Attachments[0].Filename != "secret-plan.pdf" {
		t.Fatalf("attachments = %+v", msgs[0].Attachments)
	}
	for _, q := range []string{"legacy sub", "s@example.com", "secret body"} {
		if got := s.SearchMessages(acct, q, 10); len(got) != 1 {
			t.Errorf("search %q returned %d results", q, len(got))
		}
	}
	if drafts := s.MessagesFor(acct, "Drafts"); len(drafts) != 1 || drafts[0].Subject != "draft subject" || drafts[0].To != "bob@example.com" {
		t.Fatalf("drafts = %+v", drafts)
	}
}

func dumpTables(t *testing.T, s *SQLiteStore, tables ...string) string {
	t.Helper()
	var b strings.Builder
	for _, table := range tables {
		rows, err := s.DB().Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				fmt.Fprintf(&b, "%v|", v)
			}
		}
		rows.Close()
	}
	return b.String()
}
