package notify

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
)

func TestFormat(t *testing.T) {
	one := []email.Message{{From: `"Petr Trefil" <petr@partner.test>`, Subject: "Digifest 2026"}}
	if title, body := Format("me@x.test", one, true); title != "Petr Trefil" || body != "Digifest 2026" {
		t.Fatalf("one: %q / %q", title, body)
	}
	if title, body := Format("me@x.test", one, false); title != "Petr Trefil" || body != "New mail in me@x.test" {
		t.Fatalf("hidden subject: %q / %q", title, body)
	}
	many := []email.Message{
		{From: "a@x.test", Subject: "s1"}, {From: "B <b@x.test>", Subject: "s2"},
		{From: "C <c@x.test>", Subject: ""}, {From: "d@x.test", Subject: "s4"}, {From: "e@x.test", Subject: "s5"},
	}
	title, body := Format("me@x.test", many, true)
	if title != "5 new messages in me@x.test" || body != "a@x.test: s1\nB: s2\nC\nand 2 more" {
		t.Fatalf("many: %q / %q", title, body)
	}
	if title, _ := Format("me@x.test", nil, true); title != "" {
		t.Fatal("notification for no messages")
	}
	// Hostile headers: escape sequences, hidden characters, newlines, length.
	evil := []email.Message{{From: "Pay‮Pal\x1b]0;x\x07 <x@evil.test>", Subject: "line1\r\nline2 " + strings.Repeat("A", 500)}}
	title, body = Format("me@x.test", evil, true)
	if strings.ContainsAny(title+body, "\x1b\r\n‮") || len([]rune(body)) > maxLine || title != "PayPal" {
		t.Fatalf("unsafe notification: %q / %q", title, body)
	}
}

func TestSendPassesTextAsArgumentsOnly(t *testing.T) {
	var name string
	var args []string
	old := run
	run = func(_ context.Context, n string, a ...string) error { name, args = n, a; return nil }
	defer func() { run = old }()

	title, body := "--help; rm -rf ~", `<a href="https://evil.test">click</a> $(id) & "x"`

	// Custom command: run directly, title and body are the last two arguments.
	if err := Send(config.NotificationsConfig{Command: []string{"my-notifier", "--urgency", "low"}}, title, body); err != nil {
		t.Fatal(err)
	}
	if name != "my-notifier" || len(args) != 4 || args[2] != title || args[3] != body {
		t.Fatalf("custom command: %s %q", name, args)
	}

	if err := Send(config.NotificationsConfig{}, title, body); err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "darwin":
		joined := strings.Join(args[:len(args)-2], " ")
		if name != "osascript" || strings.Contains(joined, "evil.test") || args[len(args)-2] != title || args[len(args)-1] != body {
			t.Fatalf("osascript: %q", args)
		}
	default:
		// "--" precedes the title, and markup in the body is escaped.
		if name != "notify-send" || args[len(args)-3] != "--" || args[len(args)-2] != title ||
			strings.Contains(args[len(args)-1], "<a ") || !strings.Contains(args[len(args)-1], "&lt;a href") {
			t.Fatalf("notify-send: %q", args)
		}
	}
	name = ""
	if Send(config.NotificationsConfig{}, "", "body") != nil || name != "" {
		t.Fatal("empty notification sent")
	}
}
