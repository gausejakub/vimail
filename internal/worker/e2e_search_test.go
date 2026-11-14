package worker_test

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

// The cache only knows bodies that were downloaded. A phrase inside a
// message that was never opened is found by asking the server, also over
// MCP, and also for mail that arrived after the last sync.
func TestServerSearchFindsUndownloadedBodies(t *testing.T) {
	env := newE2E(t)
	raw := func(id, subject, body string) []byte {
		return []byte("From: Petr <petr@partner.test>\r\nTo: " + me + "\r\nSubject: " + subject +
			"\r\nMessage-ID: <" + id + "@partner.test>\r\nDate: Thu, 12 Nov 2026 10:00:00 +0100\r\n\r\n" + body + "\r\n")
	}
	env.imap.appendRaw(t, me, "INBOX", raw("m1", "Quarterly numbers", "The zeppelin invoice is attached."), imap.FlagSeen)
	env.imap.appendRaw(t, me, "INBOX", raw("m2", "Lunch", "Friday at noon?"), imap.FlagSeen)
	env.imap.appendRaw(t, me, "Sent", raw("m3", "Re: Quarterly numbers", "Thanks, the zeppelin one is paid."), imap.FlagSeen)
	env.sync(t, me)

	// Headers only are cached: the phrase is not found locally.
	if got := env.store.SearchMessages(me, "zeppelin", 10); len(got) != 0 {
		t.Fatalf("local search found %d results without bodies", len(got))
	}
	res := env.coord.SearchServerNow(me, "zeppelin", 10)
	if len(res.Errors) != 0 {
		t.Fatalf("server search errors: %v", res.Errors)
	}
	var subjects []string
	for _, m := range res.Messages {
		subjects = append(subjects, m.Folder+":"+m.Subject)
	}
	got := strings.Join(subjects, "|")
	if len(res.Messages) != 2 || !strings.Contains(got, "Inbox:Quarterly numbers") || !strings.Contains(got, "Sent:Re: Quarterly numbers") {
		t.Fatalf("server results = %s", got)
	}

	// Mail that arrived after the last sync is found, cached, and the
	// incremental-sync watermark still covers everything.
	env.imap.appendRaw(t, me, "INBOX", raw("m4", "Other", "nothing"), imap.FlagSeen)
	env.imap.appendRaw(t, me, "INBOX", raw("m5", "Late", "another zeppelin"), imap.FlagSeen)
	res = env.coord.SearchServerNow(me, "zeppelin", 10)
	if len(res.Messages) != 3 {
		t.Fatalf("after new mail: %d results", len(res.Messages))
	}
	if n := len(env.store.MessagesFor(me, "Inbox")); n != 4 {
		t.Fatalf("inbox cache has %d messages, want 4 (no gap below the newest hit)", n)
	}

	// Over MCP: cache only by default, cache+server on request.
	session := env.mcpSession(t)
	var out struct {
		Searched string `json:"searched"`
		Messages []struct {
			Subject string `json:"subject"`
			Folder  string `json:"folder"`
			UID     uint32 `json:"uid"`
		} `json:"messages"`
		ServerErrors []string `json:"server_errors"`
	}
	callTool(t, session, "search_messages", map[string]any{"account": me, "query": "zeppelin"}, &out)
	if out.Searched != "cache" || len(out.Messages) != 0 {
		t.Fatalf("default search: %+v", out)
	}
	callTool(t, session, "search_messages", map[string]any{"account": me, "query": "zeppelin", "server": true}, &out)
	if out.Searched != "cache+server" || len(out.Messages) != 3 || len(out.ServerErrors) != 0 {
		t.Fatalf("server search over MCP: %+v", out)
	}
	for _, m := range out.Messages {
		if m.UID == 0 || m.Folder == "" {
			t.Fatalf("result without a handle: %+v", m)
		}
	}
	// A header hit found by both searches appears once.
	callTool(t, session, "search_messages", map[string]any{"account": me, "query": "Quarterly", "server": true}, &out)
	if len(out.Messages) != 2 {
		t.Fatalf("duplicate hits: %+v", out.Messages)
	}
}
