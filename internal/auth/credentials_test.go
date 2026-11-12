package auth

import (
	"testing"
	"time"
)

func TestAccessTokenRefreshesNearExpiry(t *testing.T) {
	calls := 0
	c := &Credentials{
		Token:  "old",
		Expiry: time.Now().Add(time.Hour),
		refresh: func() (*Credentials, error) {
			calls++
			return &Credentials{Token: "new", Expiry: time.Now().Add(time.Hour)}, nil
		},
	}
	if tok, err := c.AccessToken(); err != nil || tok != "old" || calls != 0 {
		t.Fatalf("fresh token = %q err=%v calls=%d, want cached old", tok, err, calls)
	}
	c.Expiry = time.Now().Add(30 * time.Second)
	if tok, err := c.AccessToken(); err != nil || tok != "new" || calls != 1 {
		t.Fatalf("expiring token = %q err=%v calls=%d, want refreshed new", tok, err, calls)
	}
	if tok, _ := c.AccessToken(); tok != "new" || calls != 1 {
		t.Fatalf("refreshed token reused = %q calls=%d", tok, calls)
	}
}
