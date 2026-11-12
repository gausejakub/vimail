package worker

import (
	"strings"
	"testing"
)

func TestXOAuth2Client(t *testing.T) {
	client := newXOAuth2Client("alice@example.com", "token")
	mechanism, initial, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	if mechanism != "XOAUTH2" || !strings.Contains(string(initial), "user=alice@example.com\x01auth=Bearer token\x01\x01") {
		t.Fatalf("mechanism=%q initial=%q", mechanism, initial)
	}
}
