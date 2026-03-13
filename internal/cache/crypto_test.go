package cache

import (
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey: %v", err)
	}

	cases := []string{
		"Hello, World!",
		"",
		"Short",
		"A longer email body with unicode: Příliš žluťoučký kůň 🐴",
		"<html><body><p>HTML content</p></body></html>",
	}

	for _, tc := range cases {
		encrypted := encrypt(key, tc)
		if tc != "" && encrypted == tc {
			t.Errorf("encrypt(%q) returned plaintext", tc)
		}
		decrypted := decrypt(key, encrypted)
		if decrypted != tc {
			t.Errorf("roundtrip failed: got %q, want %q", decrypted, tc)
		}
	}
}

func TestDecryptPlaintext(t *testing.T) {
	key, _ := GenerateEncryptionKey()

	// Unencrypted data should pass through unchanged.
	plain := "not encrypted at all"
	if got := decrypt(key, plain); got != plain {
		t.Errorf("decrypt(plaintext) = %q, want %q", got, plain)
	}
}

func TestEncryptNilKey(t *testing.T) {
	// Nil key should return plaintext (encryption disabled).
	text := "some body text"
	if got := encrypt(nil, text); got != text {
		t.Errorf("encrypt(nil key) = %q, want %q", got, text)
	}
	if got := decrypt(nil, text); got != text {
		t.Errorf("decrypt(nil key) = %q, want %q", got, text)
	}
}

func TestEncryptWrongKey(t *testing.T) {
	key1, _ := GenerateEncryptionKey()
	key2, _ := GenerateEncryptionKey()

	encrypted := encrypt(key1, "secret message")
	// Decrypting with wrong key should return the encrypted value unchanged.
	decrypted := decrypt(key2, encrypted)
	if decrypted == "secret message" {
		t.Error("decrypt with wrong key should not return plaintext")
	}
}

func TestEncryptedStoreRoundTrip(t *testing.T) {
	s := testStore(t)
	key, _ := GenerateEncryptionKey()
	s.SetEncryptionKey(key)

	seedWithFolders(t, s, "Test", "test@example.com")
	s.UpsertMessage("test@example.com", "Inbox", email.Message{
		UID: 1, From: "a@b.com", Subject: "Test", Date: time.Now(),
	})

	body := "This is secret email content"
	htmlBody := "<p>This is secret HTML</p>"
	s.UpdateMessageBody("test@example.com", "Inbox", 1, body, htmlBody, nil)

	// Verify it's encrypted in the DB.
	var rawBody, rawHTML string
	s.db.QueryRow(`SELECT body, html_body FROM messages WHERE uid = 1`).Scan(&rawBody, &rawHTML)
	if rawBody == body {
		t.Error("body should be encrypted in DB")
	}
	if rawHTML == htmlBody {
		t.Error("html_body should be encrypted in DB")
	}

	// Verify it decrypts correctly when read through the store.
	msgs := s.MessagesFor("test@example.com", "Inbox")
	if len(msgs) == 0 {
		t.Fatal("no messages returned")
	}
	if msgs[0].Body != body {
		t.Errorf("Body = %q, want %q", msgs[0].Body, body)
	}
	if msgs[0].HTMLBody != htmlBody {
		t.Errorf("HTMLBody = %q, want %q", msgs[0].HTMLBody, htmlBody)
	}
}
