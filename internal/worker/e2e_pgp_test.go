package worker_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/pgp/pgptest"
	"github.com/gausejakub/vimail/internal/worker"
)

// An encrypted and signed message from draft to delivery: the draft copy on
// the server is readable by its author only, the transmitted message shows
// neither text nor Bcc, the recipient reads it with a checked signature,
// and decrypted text is not written to an unencrypted cache.
func TestEncryptedSignedMailEndToEnd(t *testing.T) {
	env := newE2E(t)
	keys := pgptest.Keyring(t, alias, partner, boss)
	env.coord.SetPGP(keys)

	secret := "the acquisition closes on Friday"
	draft := email.Draft{
		ID: "d-pgp", From: "Jakub Gause <" + alias + ">", To: []string{partner}, Bcc: []string{boss},
		Subject: "Plan", Body: secret, Sign: true, Encrypt: true,
	}
	if err := env.store.SaveDraft(me, draft); err != nil {
		t.Fatal(err)
	}
	if err := env.coord.FlushDraftOpsNow(me); err != nil {
		t.Fatalf("draft upload: %v", err)
	}

	// The server draft: encrypted, and only to the author.
	drafts := env.imap.messages(t, me, "Drafts")
	if len(drafts) != 1 || bytes.Contains(drafts[0].raw, []byte(secret)) ||
		!strings.HasPrefix(headerLine(drafts[0].raw, "Content-Type"), "multipart/encrypted") {
		t.Fatalf("server draft is not encrypted: %d drafts", len(drafts))
	}
	partnerOnly := pgptest.Keyring(t, partner)
	if res, _ := worker.ParseBodyPGP(drafts[0].raw, partnerOnly); res.Decrypted || strings.Contains(res.Text, secret) {
		t.Fatal("the recipient's key opens the draft")
	}
	// Another vimail of the same user (same keys) imports it with its content and choices.
	other := cache.NewSQLiteStore(openMemory(t))
	if err := other.SeedAccount("Jakub", me, "127.0.0.1", env.imap.port, "127.0.0.1", env.smtp.port); err != nil {
		t.Fatal(err)
	}
	otherCoord := worker.NewCoordinator(config.Config{Accounts: env.cfg.Accounts[:1]}, other)
	otherCoord.SetTestCredentials(me, testCreds(me))
	otherCoord.SetPGP(keys)
	t.Cleanup(otherCoord.DisconnectAll)
	if _, err := otherCoord.SyncAccountNow(me); err != nil {
		t.Fatal(err)
	}
	imported := other.Drafts(me)
	if len(imported) != 1 || imported[0].Body != secret || !imported[0].Sign || !imported[0].Encrypt || len(imported[0].Bcc) != 1 {
		t.Fatalf("imported draft = %+v", imported)
	}
	// Without the keys the encrypted draft is not imported as an empty one.
	blind := cache.NewSQLiteStore(openMemory(t))
	blind.SeedAccount("Jakub", me, "127.0.0.1", env.imap.port, "127.0.0.1", env.smtp.port)
	blindCoord := worker.NewCoordinator(config.Config{Accounts: env.cfg.Accounts[:1]}, blind)
	blindCoord.SetTestCredentials(me, testCreds(me))
	t.Cleanup(blindCoord.DisconnectAll)
	if _, err := blindCoord.SyncAccountNow(me); err != nil {
		t.Fatal(err)
	}
	if n := len(blind.Drafts(me)); n != 0 {
		t.Fatalf("%d drafts imported without being able to decrypt", n)
	}

	// Send it.
	saved, _ := env.store.Draft(me, "d-pgp")
	if res := env.coord.SendNow(me, worker.DraftSendRequest(saved, "")); res.Err != nil {
		t.Fatalf("send: %v", res.Err)
	}
	sent := env.smtp.messages()
	if len(sent) != 1 {
		t.Fatalf("%d messages sent", len(sent))
	}
	wire := sent[0].data
	if bytes.Contains(wire, []byte(secret)) || bytes.Contains(wire, []byte(boss)) || !strings.HasPrefix(headerLine(wire, "Content-Type"), "multipart/encrypted") {
		t.Fatalf("transmitted message leaks:\n%s", headerOf(wire))
	}
	if !strings.Contains(strings.Join(sent[0].rcpts, ","), boss) {
		t.Fatalf("Bcc recipient not in the envelope: %v", sent[0].rcpts)
	}
	sentCopies := env.imap.messages(t, me, "Sent")
	if len(sentCopies) != 1 || bytes.Contains(sentCopies[0].raw, []byte(secret)) || !strings.Contains(headerLine(sentCopies[0].raw, "Bcc"), boss) {
		t.Fatal("Sent copy missing, in the clear, or without its Bcc header")
	}

	// The recipient reads it: decrypted, signature checked, verdict kept
	// outside the text.
	env.imap.appendRaw(t, partner, "INBOX", wire)
	pstore := cache.NewSQLiteStore(openMemory(t))
	pacct := env.cfg.Accounts[0]
	pacct.Name, pacct.Email, pacct.Username, pacct.Identities = "Petr", partner, partner, nil
	pstore.SeedAccount("Petr", partner, pacct.IMAPHost, pacct.IMAPPort, pacct.SMTPHost, pacct.SMTPPort)
	pcoord := worker.NewCoordinator(config.Config{Accounts: []config.AccountConfig{pacct}}, pstore)
	pcoord.SetTestCredentials(partner, testCreds(partner))
	pcoord.SetPGP(keys)
	t.Cleanup(pcoord.DisconnectAll)
	if _, err := pcoord.SyncAccountNow(partner); err != nil {
		t.Fatal(err)
	}
	inbox := pstore.MessagesFor(partner, "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("partner inbox = %d", len(inbox))
	}
	body, err := pcoord.PeekBodyNow(partner, "Inbox", inbox[0].UID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Text, secret) || !strings.HasPrefix(body.Security, "encrypted; signed by Test <"+alias+">") || strings.Contains(body.Text, "signed by") {
		t.Fatalf("recipient view: text=%q security=%q", body.Text, body.Security)
	}
	// This cache is not encrypted: the plaintext is not written to it, the
	// verdict is.
	cached, _, _ := pstore.MessageByUID(partner, "Inbox", inbox[0].UID)
	if strings.Contains(cached.Body, secret) || !strings.HasPrefix(cached.Security, "encrypted; signed by") {
		t.Fatalf("unencrypted cache: body=%q security=%q", cached.Body, cached.Security)
	}
	// With cache encryption on, it is kept.
	key, _ := cache.GenerateEncryptionKey()
	pstore.SetEncryptionKey(key)
	if _, err := pcoord.PeekBodyNow(partner, "Inbox", inbox[0].UID); err != nil {
		t.Fatal(err)
	}
	if cached, _, _ := pstore.MessageByUID(partner, "Inbox", inbox[0].UID); !strings.Contains(cached.Body, secret) {
		t.Fatalf("encrypted cache does not hold the text: %q", cached.Body)
	}

	// A recipient without a key: the send fails before anything is transmitted.
	bad := worker.SendRequest{From: alias, ToList: []string{"nokey@nowhere.test"}, Subject: "x", Body: "y", Encrypt: true}
	if res := env.coord.SendNow(me, bad); res.Err == nil || !strings.Contains(res.Err.Error(), "nokey@nowhere.test") {
		t.Fatalf("send without a key: %v", res.Err)
	}
	if n := len(env.smtp.messages()); n != 1 {
		t.Fatalf("%d messages transmitted, want 1", n)
	}
}
