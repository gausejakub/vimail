package mcp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
)

const aliasAddr = "alice@work.test"

// draftCfg configures the test account with an alias identity.
func draftCfg(attachmentDirs ...string) config.Config {
	return config.Config{
		Accounts: []config.AccountConfig{{Name: "Alice", Email: testAcct, Identities: []string{"Alice Work <" + aliasAddr + ">"}}},
		MCP:      config.MCPConfig{AllowWrite: true, AttachmentDirs: attachmentDirs},
	}
}

// seedThread adds a message (Inbox UID 10) sent to the alias, with Cc,
// Reply-To and threading headers, and a Sent message with a Bcc (UID 20).
func seedThread(t *testing.T, s *cache.SQLiteStore) {
	t.Helper()
	orig := email.Message{
		UID: 10, MessageID: "orig@partner.test", From: "Petr <petr@partner.test>",
		ToList:     []string{"Alice Work <" + aliasAddr + ">", "Eva <eva@partner.test>"},
		Cc:         []string{"Senk <senk@partner.test>", testAcct},
		ReplyTo:    []string{"Team <team@partner.test>"},
		InReplyTo:  "root@partner.test",
		References: []string{"root@partner.test"},
		Subject:    "Digifest", Date: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC), Answered: true,
	}
	if err := s.UpsertMessage(testAcct, "Inbox", orig); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMessageBody(testAcct, "Inbox", 10, "Are you coming?", "", nil); err != nil {
		t.Fatal(err)
	}
	sent := email.Message{UID: 20, MessageID: "sent@example.com", From: testAcct, ToList: []string{"x@partner.test"},
		Bcc: []string{"boss@hidden.test"}, Subject: "s", Date: time.Now()}
	if err := s.UpsertMessage(testAcct, "Sent", sent); err != nil {
		t.Fatal(err)
	}
}

func TestSaveDraftRecipientLists(t *testing.T) {
	store := seededStore(t)
	session := connectCfg(t, store, draftCfg())

	var out saveDraftResult
	call(t, session, "save_draft", map[string]any{
		"to":      []string{"a@x.test", "Bob <b@x.test>"},
		"cc":      []string{`"Doe, John" <j@x.test>`, "c@x.test, d@x.test"},
		"bcc":     []string{"e@x.test"},
		"subject": "multi", "body": "hi",
	}, &out)
	d := out.Draft
	if !reflect.DeepEqual(d.To, []string{"a@x.test", "Bob <b@x.test>"}) ||
		!reflect.DeepEqual(d.Cc, []string{`"Doe, John" <j@x.test>`, "c@x.test", "d@x.test"}) ||
		!reflect.DeepEqual(d.Bcc, []string{"e@x.test"}) {
		t.Fatalf("draft = %+v", d)
	}
	// The stored draft has the same fields (what the TUI will open).
	stored, _ := store.Draft(testAcct, out.ID)
	if !reflect.DeepEqual(stored.Cc, d.Cc) || !reflect.DeepEqual(stored.Bcc, d.Bcc) {
		t.Fatalf("stored = %+v", stored)
	}
	// Without a reachable server the upload is queued, and the result says so.
	if out.ServerSync != "queued" || out.ServerSyncError == "" {
		t.Fatalf("server_sync = %q %q", out.ServerSync, out.ServerSyncError)
	}

	// Older callers pass to as one string.
	call(t, session, "save_draft", map[string]any{"to": "a@x.test, Bob <b@x.test>"}, &out)
	if !reflect.DeepEqual(out.Draft.To, []string{"a@x.test", "Bob <b@x.test>"}) {
		t.Fatalf("string to = %q", out.Draft.To)
	}

	// Updating keeps fields that are not passed.
	id := out.ID
	out = saveDraftResult{} // Unmarshal keeps fields the JSON omits
	call(t, session, "save_draft", map[string]any{"id": id, "subject": "now with subject"}, &out)
	if out.ID != id || out.Draft.Subject != "now with subject" || len(out.Draft.To) != 2 {
		t.Fatalf("update = %+v", out.Draft)
	}
	if callErr(t, session, "save_draft", map[string]any{"cc": []string{"nope"}}) {
		return
	}
	t.Fatal("invalid cc accepted")
}

func TestSaveDraftReplyThreadingAndIdentity(t *testing.T) {
	store := seededStore(t)
	seedThread(t, store)
	session := connectCfg(t, store, draftCfg())

	var out saveDraftResult
	call(t, session, "save_draft", map[string]any{
		"reply_to_message": map[string]any{"account": testAcct, "folder": "Inbox", "uid": 10},
		"quote_original":   true,
		"body":             "Yes.",
	}, &out)
	d := out.Draft
	if d.Subject != "Re: Digifest" || d.InReplyTo != "orig@partner.test" ||
		!reflect.DeepEqual(d.References, []string{"root@partner.test", "orig@partner.test"}) {
		t.Fatalf("threading = %+v", d)
	}
	if !reflect.DeepEqual(d.To, []string{"Team <team@partner.test>"}) || len(d.Cc) != 0 {
		t.Fatalf("reply goes to Reply-To only: to=%q cc=%q", d.To, d.Cc)
	}
	if d.From != "Alice Work <"+aliasAddr+">" {
		t.Fatalf("From = %q, want the alias the original was sent to", d.From)
	}
	if !strings.HasPrefix(d.Body, "Yes.\n\nOn ") || !strings.Contains(d.Body, "> Are you coming?") {
		t.Fatalf("body = %q", d.Body)
	}
	if d.Source == nil || d.Source.Kind != "reply" || d.Source.Folder != "Inbox" || d.Source.UID != 10 {
		t.Fatalf("source = %+v", d.Source)
	}

	// Reply-all: To and Cc of the original minus the account and its alias.
	call(t, session, "save_draft", map[string]any{
		"reply_to_message": map[string]any{"folder": "Inbox", "uid": 10}, "reply_all": true,
	}, &out)
	if !reflect.DeepEqual(out.Draft.To, []string{"Team <team@partner.test>"}) ||
		!reflect.DeepEqual(out.Draft.Cc, []string{"Eva <eva@partner.test>", "Senk <senk@partner.test>"}) {
		t.Fatalf("reply-all to=%q cc=%q", out.Draft.To, out.Draft.Cc)
	}
	// An explicit subject and To override the computed ones.
	call(t, session, "save_draft", map[string]any{
		"reply_to_message": map[string]any{"folder": "Inbox", "uid": 10}, "subject": "Custom", "to": "petr@partner.test",
	}, &out)
	if out.Draft.Subject != "Custom" || !reflect.DeepEqual(out.Draft.To, []string{"petr@partner.test"}) || out.Draft.InReplyTo != "orig@partner.test" {
		t.Fatalf("override = %+v", out.Draft)
	}

	// From is validated against the account's identities.
	out = saveDraftResult{} // Unmarshal keeps fields the JSON omits
	call(t, session, "save_draft", map[string]any{"to": "x@y.test", "from": testAcct}, &out)
	if out.Draft.From != "Alice <"+testAcct+">" { // the configured form, with display name
		t.Fatalf("primary from = %q", out.Draft.From)
	}
	out = saveDraftResult{} // Unmarshal keeps fields the JSON omits
	call(t, session, "save_draft", map[string]any{"to": "x@y.test", "from": aliasAddr}, &out)
	if out.Draft.From != "Alice Work <"+aliasAddr+">" {
		t.Fatalf("alias from = %q", out.Draft.From)
	}
	if !callErr(t, session, "save_draft", map[string]any{"to": "x@y.test", "from": "ceo@corp.test"}) {
		t.Fatal("a From that is not an identity was accepted")
	}

	// Forward: subject, inline original, recipients chosen by the caller.
	call(t, session, "save_draft", map[string]any{
		"forward_message": map[string]any{"folder": "Inbox", "uid": 10}, "to": []string{"z@x.test"}, "body": "FYI",
	}, &out)
	if out.Draft.Subject != "Fwd: Digifest" || out.Draft.InReplyTo != "" || !strings.Contains(out.Draft.Body, "---------- Forwarded message ----------") ||
		!strings.Contains(out.Draft.Body, "Are you coming?") || out.Draft.Source.Kind != "forward" {
		t.Fatalf("forward = %+v", out.Draft)
	}
	if !callErr(t, session, "save_draft", map[string]any{"reply_to_message": map[string]any{"folder": "Inbox", "uid": 999}}) {
		t.Fatal("reply to a missing message accepted")
	}
}

func TestReadOutputCarriesFullHeaders(t *testing.T) {
	store := seededStore(t)
	seedThread(t, store)
	session := connectCfg(t, store, draftCfg())

	var list listMessagesResult
	call(t, session, "list_messages", map[string]any{"folder": "Inbox"}, &list)
	var h *messageHeader
	for i := range list.Messages {
		if list.Messages[i].UID == 10 {
			h = &list.Messages[i]
		}
	}
	if h == nil {
		t.Fatal("message 10 not listed")
	}
	if len(h.To) != 2 || len(h.Cc) != 2 || h.ReplyTo[0] != "Team <team@partner.test>" || h.MessageID != "orig@partner.test" ||
		h.InReplyTo != "root@partner.test" || h.ThreadID != "root@partner.test" || !h.Answered || h.Bcc != nil {
		t.Fatalf("list header = %+v", *h)
	}

	var read readMessageResult
	call(t, session, "read_message", map[string]any{"folder": "Inbox", "uid": 10}, &read)
	if len(read.Cc) != 2 || read.ReplyTo == nil || read.MessageID != "orig@partner.test" || len(read.References) != 1 {
		t.Fatalf("read header = %+v", read.messageHeader)
	}
	var batch readMessagesResult
	call(t, session, "read_messages", map[string]any{"messages": []map[string]any{{"account": testAcct, "folder": "Sent", "uid": 20}}}, &batch)
	if len(batch.Messages) != 1 || !reflect.DeepEqual(batch.Messages[0].Bcc, []string{"boss@hidden.test"}) {
		t.Fatalf("Sent message should expose its Bcc: %+v", batch.Messages)
	}
	var search searchMessagesResult
	call(t, session, "search_messages", map[string]any{"query": "Digifest"}, &search)
	if len(search.Messages) != 1 || len(search.Messages[0].Cc) != 2 || search.Messages[0].MessageID == "" {
		t.Fatalf("search = %+v", search.Messages)
	}
	var recent listRecentMessagesResult
	call(t, session, "list_recent_messages", map[string]any{"since": "2026-11-01T00:00:00Z", "until": "2026-11-30T00:00:00Z"}, &recent)
	if len(recent.Messages) != 1 || len(recent.Messages[0].Cc) != 2 || !recent.Messages[0].Answered {
		t.Fatalf("recent = %+v", recent.Messages)
	}

	var accounts listAccountsResult
	call(t, session, "list_accounts", nil, &accounts)
	if len(accounts.Accounts) != 1 || !reflect.DeepEqual(accounts.Accounts[0].Identities, []string{"Alice Work <" + aliasAddr + ">"}) {
		t.Fatalf("accounts = %+v", accounts.Accounts)
	}
}

func TestReadDraftReturnsEverySavedField(t *testing.T) {
	store := seededStore(t)
	seedThread(t, store)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "program.pdf"), []byte("%PDF fake"), 0600); err != nil {
		t.Fatal(err)
	}
	session := connectCfg(t, store, draftCfg(dir))

	var saved saveDraftResult
	call(t, session, "save_draft", map[string]any{
		"reply_to_message": map[string]any{"folder": "Inbox", "uid": 10}, "reply_all": true,
		"bcc": []string{"boss@hidden.test"}, "body": "hi", "attachments": []string{filepath.Join(dir, "program.pdf")},
	}, &saved)

	var list listMessagesResult
	call(t, session, "list_messages", map[string]any{"folder": "Drafts"}, &list)
	if len(list.Messages) != 1 || list.Messages[0].DraftID != saved.ID || list.Messages[0].Bcc == nil {
		t.Fatalf("drafts listing = %+v", list.Messages)
	}

	var d draftInfo
	call(t, session, "read_draft", map[string]any{"id": saved.ID}, &d)
	if !reflect.DeepEqual(d, saved.Draft) {
		t.Fatalf("read_draft differs from save_draft result:\n%+v\n%+v", d, saved.Draft)
	}
	if d.From == "" || len(d.To) == 0 || len(d.Cc) == 0 || len(d.Bcc) != 1 || d.Subject == "" || d.Body == "" || d.InReplyTo == "" ||
		len(d.References) == 0 || d.Source == nil || len(d.Attachments) != 1 || d.Attachments[0].Filename != "program.pdf" ||
		d.Attachments[0].ContentType != "application/pdf" || d.Attachments[0].Size != 9 {
		t.Fatalf("read_draft = %+v", d)
	}
	if !callErr(t, session, "read_draft", map[string]any{"id": "missing"}) {
		t.Fatal("missing draft read")
	}
}

func TestSaveDraftAttachmentAllowlist(t *testing.T) {
	store := seededStore(t)
	allowed := t.TempDir()
	outside := t.TempDir()
	write := func(dir, name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	inside := write(allowed, "ok.txt")
	secret := write(outside, "id_ed25519")
	link := filepath.Join(allowed, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}

	// Disabled unless the user configured attachment_dirs.
	if !callErr(t, connectCfg(t, store, draftCfg()), "save_draft", map[string]any{"to": "a@x.test", "attachments": []string{inside}}) {
		t.Fatal("attachment accepted without attachment_dirs")
	}

	session := connectCfg(t, store, draftCfg(allowed))
	var out saveDraftResult
	out = saveDraftResult{} // Unmarshal keeps fields the JSON omits
	call(t, session, "save_draft", map[string]any{"to": "a@x.test", "attachments": []string{inside}}, &out)
	if len(out.Draft.Attachments) != 1 || out.Draft.Attachments[0].Filename != "ok.txt" {
		t.Fatalf("attachments = %+v", out.Draft.Attachments)
	}
	for _, p := range []string{secret, link, filepath.Join(allowed, "..", filepath.Base(outside), "id_ed25519"), allowed} {
		if !callErr(t, session, "save_draft", map[string]any{"to": "a@x.test", "attachments": []string{p}}) {
			t.Errorf("attachment %s accepted", p)
		}
	}
	// [] removes attachments; omitting the field keeps them.
	id := out.ID
	out = saveDraftResult{} // Unmarshal keeps fields the JSON omits
	call(t, session, "save_draft", map[string]any{"id": id, "subject": "keep"}, &out)
	if len(out.Draft.Attachments) != 1 {
		t.Fatal("attachments dropped by an update that did not mention them")
	}
	var cleared saveDraftResult // fresh: Unmarshal keeps fields the JSON omits
	call(t, session, "save_draft", map[string]any{"id": id, "attachments": []string{}}, &cleared)
	if len(cleared.Draft.Attachments) != 0 {
		t.Fatal("attachments: [] did not remove them")
	}
}

// errText returns the tool's error message ("" when the call succeeded).
func errText(t *testing.T, session *sdk.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if !res.IsError {
		return ""
	}
	var texts []string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, "; ")
}

// The identity check must not be passed by a From that hides a second
// mailbox, including when the matching identity is configured as a bare
// address (the case where the caller's string is stored as given).
func TestSaveDraftFromCannotSmuggleASecondMailbox(t *testing.T) {
	store := seededStore(t)
	cfg := config.Config{
		Accounts: []config.AccountConfig{{Email: testAcct, Identities: []string{aliasAddr}}}, // no names: bare identities
		MCP:      config.MCPConfig{AllowWrite: true},
	}
	session := connectCfg(t, store, cfg)
	for _, from := range []string{
		`x <"ceo@corp.test>, <alice"@example.com>`,
		`x <"ceo@corp.test>, <alice"@work.test>`,
		`x <ceo@corp.test>, <alice@example.com>`,
		`ceo@corp.test, alice@example.com`,
	} {
		if msg := errText(t, session, "save_draft", map[string]any{"to": "a@x.test", "from": from}); msg == "" {
			t.Errorf("from %q accepted", from)
		}
	}
	if n := len(store.Drafts(testAcct)); n != 0 {
		t.Fatalf("%d drafts stored by rejected calls", n)
	}
	var out saveDraftResult
	call(t, session, "save_draft", map[string]any{"to": "a@x.test", "from": "Alice <" + aliasAddr + ">"}, &out)
	if out.Draft.From != "Alice <"+aliasAddr+">" {
		t.Fatalf("valid identity: from = %q", out.Draft.From)
	}
}

// A path outside the allowed directories gets the same answer whether or
// not it exists, so the tool cannot be used to probe the file system.
func TestAttachmentErrorsDoNotRevealFileExistence(t *testing.T) {
	store := seededStore(t)
	allowed, outside := t.TempDir(), t.TempDir()
	existing := filepath.Join(outside, "id_ed25519")
	if err := os.WriteFile(existing, []byte("k"), 0600); err != nil {
		t.Fatal(err)
	}
	session := connectCfg(t, store, draftCfg(allowed))
	msg := func(p string) string {
		m := errText(t, session, "save_draft", map[string]any{"to": "a@x.test", "attachments": []string{p}})
		if m == "" {
			t.Fatalf("attachment %s accepted", p)
		}
		return strings.ReplaceAll(m, p, "PATH")
	}
	exists, missing := msg(existing), msg(filepath.Join(outside, "no-such-file"))
	if exists != missing {
		t.Fatalf("errors differ:\n existing: %s\n missing:  %s", exists, missing)
	}
	if strings.Contains(exists, "no such file") {
		t.Fatalf("OS error leaked: %s", exists)
	}
}

// Draft output must not carry an unsanitized content type.
func TestDraftOutputSanitizesAttachmentContentType(t *testing.T) {
	store := seededStore(t)
	hidden := string(rune(0xE0049)) + string(rune(0xE0047))
	if err := store.SaveDraft(testAcct, email.Draft{ID: "d-ct", To: []string{"a@x.test"},
		Attachments: []email.DraftAttachment{{Filename: "f.pdf", ContentType: "application/pdf" + hidden + "\x1b[2J", Data: []byte("x")}}}); err != nil {
		t.Fatal(err)
	}
	var d draftInfo
	call(t, connectCfg(t, store, draftCfg()), "read_draft", map[string]any{"id": "d-ct"}, &d)
	if len(d.Attachments) != 1 || d.Attachments[0].ContentType != "application/pdf" {
		t.Fatalf("content_type = %q", d.Attachments[0].ContentType)
	}
}

func TestSaveDraftMarkdown(t *testing.T) {
	store := seededStore(t)
	cfg := draftCfg()
	cfg.Accounts[0].Markdown = true
	session := connectCfg(t, store, cfg)

	var out saveDraftResult
	call(t, session, "save_draft", map[string]any{"to": "a@x.test", "body": "**hi**"}, &out)
	if !out.Draft.Markdown {
		t.Fatal("account default not applied to a new draft")
	}
	id := out.ID
	out = saveDraftResult{}
	call(t, session, "save_draft", map[string]any{"id": id, "markdown": false}, &out)
	if out.Draft.Markdown {
		t.Fatal("markdown=false ignored")
	}
	out = saveDraftResult{}
	call(t, session, "save_draft", map[string]any{"id": id, "subject": "s"}, &out)
	if out.Draft.Markdown {
		t.Fatal("update without markdown changed the setting")
	}
	if d, _ := store.Draft(testAcct, id); d.Markdown {
		t.Fatal("stored draft is Markdown")
	}
	// Without the account setting, plain unless asked.
	session = connectCfg(t, store, draftCfg())
	out = saveDraftResult{}
	call(t, session, "save_draft", map[string]any{"to": "a@x.test", "body": "x"}, &out)
	if out.Draft.Markdown {
		t.Fatal("plain account produced a Markdown draft")
	}
	out = saveDraftResult{}
	call(t, session, "save_draft", map[string]any{"to": "a@x.test", "body": "x", "markdown": true}, &out)
	if d, _ := store.Draft(testAcct, out.ID); !out.Draft.Markdown || !d.Markdown {
		t.Fatal("markdown=true not stored")
	}
}

func TestSaveDraftSignAndEncrypt(t *testing.T) {
	store := seededStore(t)
	// OpenPGP off: asking for it is an error, and nothing is stored.
	session := connectCfg(t, store, draftCfg())
	if !callErr(t, session, "save_draft", map[string]any{"to": "a@x.test", "body": "x", "encrypt": true}) {
		t.Fatal("encrypt accepted without OpenPGP enabled")
	}
	if n := len(store.Drafts(testAcct)); n != 0 {
		t.Fatalf("%d drafts stored", n)
	}

	cfg := draftCfg()
	cfg.PGP.Enabled = true
	cfg.PGP.GPG = "/nonexistent/gpg" // this test must never reach a real keyring
	cfg.Accounts[0].PGPSign = true
	session = connectCfg(t, store, cfg)
	var out saveDraftResult
	call(t, session, "save_draft", map[string]any{"to": "a@x.test", "body": "x"}, &out)
	if !out.Draft.Sign || out.Draft.Encrypt {
		t.Fatalf("account defaults: %+v", out.Draft)
	}
	id := out.ID
	out = saveDraftResult{}
	call(t, session, "save_draft", map[string]any{"id": id, "encrypt": true, "sign": false}, &out)
	if d, _ := store.Draft(testAcct, id); out.Draft.Sign || !out.Draft.Encrypt || d.Sign || !d.Encrypt {
		t.Fatalf("explicit choice: %+v stored=%+v", out.Draft, d)
	}
	var read draftInfo
	call(t, session, "read_draft", map[string]any{"id": id}, &read)
	if !read.Encrypt || read.Sign {
		t.Fatalf("read_draft: %+v", read)
	}
}
