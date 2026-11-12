package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
