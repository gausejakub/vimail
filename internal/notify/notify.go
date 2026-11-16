// Package notify shows a desktop notification when new mail arrives.
//
// What it shows comes from message headers, which are third-party text:
// it is sanitized, shortened, and handed to the notifier as separate
// arguments, never through a shell or a script string.
package notify

import (
	"context"
	"fmt"
	"html"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
)

const (
	maxTitle = 80
	maxLine  = 100
	maxLines = 3
)

// Format builds the notification for new messages in one account.
// With showSubject false only the senders are named.
func Format(account string, msgs []email.Message, showSubject bool) (title, body string) {
	if len(msgs) == 0 {
		return "", ""
	}
	line := func(m email.Message) string {
		from := senderName(m.From)
		if !showSubject || strings.TrimSpace(m.Subject) == "" {
			return from
		}
		return from + ": " + clean(m.Subject)
	}
	if len(msgs) == 1 {
		title = senderName(msgs[0].From)
		if showSubject {
			body = clean(msgs[0].Subject)
		}
		if body == "" {
			body = "New mail in " + clean(account)
		}
		return shorten(title, maxTitle), shorten(body, maxLine)
	}
	title = fmt.Sprintf("%d new messages in %s", len(msgs), clean(account))
	var lines []string
	for i, m := range msgs {
		if i == maxLines {
			lines = append(lines, fmt.Sprintf("and %d more", len(msgs)-maxLines))
			break
		}
		lines = append(lines, shorten(line(m), maxLine))
	}
	return shorten(title, maxTitle), strings.Join(lines, "\n")
}

// senderName returns the display name of a From value, or its address.
func senderName(from string) string {
	from = clean(from)
	if i := strings.LastIndex(from, "<"); i > 0 {
		if name := strings.Trim(strings.TrimSpace(from[:i]), `"`); name != "" {
			return name
		}
	}
	if from == "" {
		return "Unknown sender"
	}
	return strings.Trim(from, "<>")
}

func clean(s string) string { return strings.TrimSpace(email.SanitizeLine(s)) }

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// run executes the notifier; tests replace it.
var run = func(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

// Send shows a notification. The configured command, when set, is run with
// title and body appended as its last two arguments; otherwise the
// platform's notifier is used.
func Send(cfg config.NotificationsConfig, title, body string) error {
	if title == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	name, args := command(runtime.GOOS, cfg, title, body)
	return run(ctx, name, args...)
}

// command returns the notifier and its arguments for a platform.
//
// Title and body are a sender's name and subject. Neither may start with
// "-": a notifier reads such an argument as an option, and osascript reads
// the argument after a "-e" as a line of AppleScript, so a sender named "-e"
// would have had its subject run as a script. The leading dashes are removed
// for every notifier (a configured command cannot be given a "--" it may not
// understand), and the built-in ones get "--" as well.
func command(goos string, cfg config.NotificationsConfig, title, body string) (string, []string) {
	title, body = plainArgument(title), plainArgument(body)
	if len(cfg.Command) > 0 {
		return cfg.Command[0], append(append([]string{}, cfg.Command[1:]...), title, body)
	}
	switch goos {
	case "darwin":
		// Title and body are passed as arguments to the script, not pasted
		// into it, so their content cannot become AppleScript.
		return "osascript", []string{
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
			"-e", "end run",
			"--", title, body}
	default:
		// Notification servers render a small HTML subset in the body; the
		// body is escaped so a subject cannot add markup or links. The
		// title is plain text by the specification, but some servers
		// render markup there too, so its angle brackets are replaced
		// rather than escaped (an escape would show literally elsewhere).
		title = strings.NewReplacer("<", "‹", ">", "›").Replace(title)
		return "notify-send", []string{"--app-name=vimail", "--icon=mail-unread", "--", title, html.EscapeString(body)}
	}
}

// plainArgument makes text safe to pass as a command-line argument that is
// not an option: it never starts with "-".
func plainArgument(s string) string {
	if t := strings.TrimLeft(s, "- \t"); t != "" {
		return t
	}
	return "(no text)"
}
