package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/config"
	"github.com/zalando/go-keyring"
)

func TestRunSecretCmdTrimsOnlyTrailingNewlines(t *testing.T) {
	got, err := RunSecretCmd(`printf ' p@ss word \n'`)
	if err != nil {
		t.Fatal(err)
	}
	if got != " p@ss word " {
		t.Fatalf("got %q, want inner and edge spaces preserved", got)
	}
}

func TestRunSecretCmdFailureReportsStderrNotStdout(t *testing.T) {
	_, err := RunSecretCmd(`echo hunter2; echo "item not found" >&2; exit 3`)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "item not found") {
		t.Fatalf("error %q should include stderr", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error %q leaks stdout", err)
	}
}

func TestRunSecretCmdEmptyOutputIsError(t *testing.T) {
	if _, err := RunSecretCmd(`printf '\n'`); err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestRunSecretCmdTimeout(t *testing.T) {
	old := secretCmdTimeout
	secretCmdTimeout = 100 * time.Millisecond
	defer func() { secretCmdTimeout = old }()
	_, err := RunSecretCmd(`sleep 5`)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestCacheKeyFromCmd(t *testing.T) {
	// 32 zero bytes, base64.
	key, err := CacheKeyFromCmd(`echo AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`)
	if err != nil || len(key) != 32 {
		t.Fatalf("key len=%d err=%v, want 32 bytes", len(key), err)
	}
	if _, err := CacheKeyFromCmd(`echo c2hvcnQ=`); err == nil {
		t.Fatal("expected error for short key")
	}
	if _, err := CacheKeyFromCmd(`echo 'not base64!'`); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestPlainResolverPrefersPasswordCmdOverKeyring(t *testing.T) {
	keyring.MockInit()
	if err := StorePassword("a@example.com", "from-keyring"); err != nil {
		t.Fatal(err)
	}
	acct := config.AccountConfig{Email: "a@example.com", AuthMethod: "plain", PasswordCmd: "echo from-cmd"}
	creds, err := (&PlainResolver{}).Resolve(acct)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Password != "from-cmd" || creds.Username != "a@example.com" {
		t.Fatalf("creds = %q/%q, want password_cmd result", creds.Username, creds.Password)
	}

	acct.PasswordCmd = ""
	creds, err = (&PlainResolver{}).Resolve(acct)
	if err != nil || creds.Password != "from-keyring" {
		t.Fatalf("keyring fallback = %v/%v", creds, err)
	}
}

func TestPlainResolverPasswordCmdFailureDoesNotFallBackToKeyring(t *testing.T) {
	keyring.MockInit()
	_ = StorePassword("b@example.com", "from-keyring")
	acct := config.AccountConfig{Email: "b@example.com", PasswordCmd: "exit 1"}
	if _, err := (&PlainResolver{}).Resolve(acct); err == nil {
		t.Fatal("expected password_cmd error, not a silent keyring fallback")
	}
}
