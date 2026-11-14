package worker_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-imap/v2"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/mcp"
	"github.com/gausejakub/vimail/internal/tui"
	"github.com/gausejakub/vimail/internal/tui/util"
	"github.com/gausejakub/vimail/internal/worker"
)

const (
	me      = "jakub@ecomail.test"  // the account; aliases deliver here too
	alias   = "jakub@lettr.test"    // identity the original was sent to
	partner = "trefil@partner.test" // the correspondent (also a vimail account, to check threading)
	senk    = "senk@partner.test"   // Cc on the original
	boss    = "boss@hidden.test"    // added as Bcc in the reply
)

// originalMessage is what the partner sent: addressed to the alias, with the
// account address and a colleague in Cc, part of an existing thread, and an
// attachment whose name is RFC 2047-encoded inside the quoted parameter.
const originalMessage = "From: Petr Trefil <trefil@partner.test>\r\n" +
	"To: Jakub <jakub@lettr.test>\r\n" +
	"Cc: Senk <senk@partner.test>, jakub@ecomail.test\r\n" +
	"Subject: Digifest 2026\r\n" +
	"Date: Thu, 12 Nov 2026 10:00:00 +0100\r\n" +
	"Message-ID: <orig-1@partner.test>\r\n" +
	"In-Reply-To: <root-0@partner.test>\r\n" +
	"References: <root-0@partner.test>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b1\"\r\n" +
	"\r\n" +
	"--b1\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Hi Jakub, are you coming to Digifest?\r\n" +
	"--b1\r\n" +
	"Content-Type: application/pdf; name=\"=?UTF-8?Q?Digifest_2026_program_=C5=99.pdf?=\"\r\n" +
	"Content-Disposition: attachment; filename=\"=?UTF-8?Q?Digifest_2026_program_=C5=99.pdf?=\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQK\r\n" +
	"--b1--\r\n"

type e2eEnv struct {
	imap  *imapTestServer
	smtp  *smtpSink
	cfg   config.Config
	store *cache.SQLiteStore
	coord *worker.Coordinator
}

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	cert, pool := testCert(t)
	t.Cleanup(worker.SetTestTLSRoots(pool))
	env := &e2eEnv{imap: startIMAP(t, cert, pool, me, partner), smtp: startSMTP(t, cert)}
	account := func(name, addr string, identities ...string) config.AccountConfig {
		return config.AccountConfig{
			Name: name, Email: addr, Username: addr, AuthMethod: "plain", TLS: "tls",
			IMAPHost: "127.0.0.1", IMAPPort: env.imap.port,
			SMTPHost: "127.0.0.1", SMTPPort: env.smtp.port,
			Identities: identities,
		}
	}
	env.cfg = config.DefaultConfig()
	env.cfg.Theme.Name = "vimail"
	env.cfg.Accounts = []config.AccountConfig{
		account("Jakub", me, "Jakub Gause <"+alias+">"),
	}
	env.cfg.MCP = config.MCPConfig{AllowWrite: true}

	db, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	env.store = cache.NewSQLiteStore(db)
	for _, a := range env.cfg.Accounts {
		if err := env.store.SeedAccount(a.Name, a.Email, a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort); err != nil {
			t.Fatal(err)
		}
	}
	env.coord = worker.NewCoordinator(env.cfg, env.store)
	for _, a := range env.cfg.Accounts {
		env.coord.SetTestCredentials(a.Email, testCreds(a.Email)) // never the keyring
	}
	t.Cleanup(env.coord.DisconnectAll)
	return env
}

func (env *e2eEnv) sync(t *testing.T, account string) {
	t.Helper()
	if _, err := env.coord.SyncAccountNow(account); err != nil {
		t.Fatalf("sync %s: %v", account, err)
	}
}

func (env *e2eEnv) mcpSession(t *testing.T) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcp.New(env.cfg, env.store, env.coord)
	serverT, clientT := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverT); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func callTool(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any, out any) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		var texts []string
		for _, c := range res.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
		t.Fatalf("%s: tool error: %s", tool, strings.Join(texts, "; "))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
}

// runCmds executes a command tree and returns the messages it produced.
// Timer commands (cursor blink, spinners) are abandoned after a timeout; the
// messages are not fed back, so no tick loop can start.
func runCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, runCmds(c)...)
			}
			return out
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(3 * time.Second):
		return nil
	}
}

func update(m tui.Model, msg tea.Msg) (tui.Model, []tea.Msg) {
	next, cmd := m.Update(msg)
	return next.(tui.Model), runCmds(cmd)
}

func keys(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func headerOf(raw []byte) string {
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	return strings.ReplaceAll(head, "\r\n ", " ")
}

func headerLine(raw []byte, name string) string {
	for _, line := range strings.Split(headerOf(raw), "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TestReplyDraftFromMCPSentFromTUIThreadsCorrectly is the full round trip:
// an MCP client prepares a reply-all draft with a Bcc, the draft is uploaded
// to the server Drafts folder, the user opens it in the TUI composer, edits
// it and sends it. The transmitted message must carry the alias as From, the
// Cc, the threading headers and no Bcc; the original must be \Answered; the
// server draft must be gone; and the partner's vimail must thread the reply
// under the original.
func TestReplyDraftFromMCPSentFromTUIThreadsCorrectly(t *testing.T) {
	env := newE2E(t)
	env.imap.appendRaw(t, me, "INBOX", []byte(originalMessage))
	env.imap.appendRaw(t, partner, "Sent", []byte(originalMessage), imap.FlagSeen)
	env.sync(t, me)

	session := env.mcpSession(t)

	// Reading: cc, reply_to, ids, threading, answered and decoded names.
	var list struct {
		Messages []struct {
			UID        uint32   `json:"uid"`
			To         []string `json:"to"`
			Cc         []string `json:"cc"`
			MessageID  string   `json:"message_id"`
			InReplyTo  string   `json:"in_reply_to"`
			References []string `json:"references"`
			ThreadID   string   `json:"thread_id"`
			Answered   bool     `json:"answered"`
		} `json:"messages"`
	}
	callTool(t, session, "list_messages", map[string]any{"account": me, "folder": "Inbox"}, &list)
	if len(list.Messages) != 1 {
		t.Fatalf("inbox = %+v", list.Messages)
	}
	orig := list.Messages[0]
	if orig.MessageID != "orig-1@partner.test" || orig.InReplyTo != "root-0@partner.test" || orig.ThreadID != "root-0@partner.test" ||
		len(orig.Cc) != 2 || orig.Answered || len(orig.To) != 1 {
		t.Fatalf("list_messages header = %+v", orig)
	}
	var read struct {
		Messages []struct {
			Attachments []string `json:"attachments"`
			Body        string   `json:"body"`
		} `json:"messages"`
	}
	callTool(t, session, "read_messages", map[string]any{
		"messages": []map[string]any{{"account": me, "folder": "Inbox", "uid": orig.UID}}, "fetch_missing": true,
	}, &read)
	if len(read.Messages) != 1 || len(read.Messages[0].Attachments) != 1 || read.Messages[0].Attachments[0] != "Digifest 2026 program ř.pdf" {
		t.Fatalf("read_messages = %+v", read.Messages)
	}

	// Drafting: a reply-all with a Bcc, quoted original, From defaulted.
	var saved struct {
		ID         string `json:"id"`
		ServerSync string `json:"server_sync"`
		Error      string `json:"server_sync_error"`
		Draft      struct {
			From       string   `json:"from"`
			To         []string `json:"to"`
			Cc         []string `json:"cc"`
			Bcc        []string `json:"bcc"`
			Subject    string   `json:"subject"`
			InReplyTo  string   `json:"in_reply_to"`
			References []string `json:"references"`
		} `json:"draft"`
	}
	callTool(t, session, "save_draft", map[string]any{
		"account":          me,
		"reply_to_message": map[string]any{"account": me, "folder": "Inbox", "uid": orig.UID},
		"reply_all":        true,
		"quote_original":   true,
		"body":             "Yes, see you there.",
		"bcc":              []string{"Boss <" + boss + ">"},
	}, &saved)
	d := saved.Draft
	if saved.ServerSync != "uploaded" {
		t.Fatalf("server_sync = %q (%s)", saved.ServerSync, saved.Error)
	}
	if d.From != "Jakub Gause <"+alias+">" || strings.Join(d.To, ",") != "Petr Trefil <"+partner+">" ||
		strings.Join(d.Cc, ",") != "Senk <"+senk+">" || strings.Join(d.Bcc, ",") != "Boss <"+boss+">" ||
		d.Subject != "Re: Digifest 2026" || d.InReplyTo != "orig-1@partner.test" ||
		strings.Join(d.References, " ") != "root-0@partner.test orig-1@partner.test" {
		t.Fatalf("draft = %+v", d)
	}

	// The server Drafts folder holds it, Bcc included (other clients show it).
	drafts := env.imap.messages(t, me, "Drafts")
	if len(drafts) != 1 || !drafts[0].has(imap.FlagDraft) || !strings.Contains(headerLine(drafts[0].raw, "Bcc"), boss) {
		t.Fatalf("server drafts = %d", len(drafts))
	}
	// A sync recognizes the uploaded copy as this draft: no duplicate.
	env.sync(t, me)
	if n := len(env.store.Drafts(me)); n != 1 {
		t.Fatalf("after sync: %d local drafts, want 1", n)
	}

	// The user opens the draft in the TUI, adds a line, and sends it.
	m := tui.WithCoordinator(tui.New(env.cfg, env.store), env.coord)
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	m, _ = update(m, util.OpenDraftMsg{Message: email.Message{ID: saved.ID}})
	view := m.View()
	for _, want := range []string{alias, senk, boss, "Re: Digifest 2026"} {
		if !strings.Contains(view, want) {
			t.Fatalf("composer does not show %q:\n%s", want, view)
		}
	}
	m, _ = update(m, keys("g"))
	m, _ = update(m, keys("g"))
	m, _ = update(m, keys("O")) // open a line above the first, insert mode
	for _, r := range "PS: edited in the TUI." {
		m, _ = update(m, keys(string(r)))
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	var submitted []tea.Msg
	m, submitted = update(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	var submit *util.ComposeSubmitMsg
	for _, msg := range submitted {
		if s, ok := msg.(util.ComposeSubmitMsg); ok {
			submit = &s
		}
	}
	if submit == nil {
		t.Fatalf("ctrl+s produced %v", submitted)
	}
	m, results := update(m, *submit)
	for _, msg := range results {
		if r, ok := msg.(worker.SendResult); ok && r.Err != nil {
			t.Fatalf("send failed: %v", r.Err)
		}
	}

	// What went over SMTP.
	sent := env.smtp.messages()
	if len(sent) != 1 {
		t.Fatalf("%d messages sent, want 1", len(sent))
	}
	out := sent[0]
	if out.from != me {
		t.Errorf("envelope sender = %q, want the account address", out.from)
	}
	rcpts := strings.Join(out.rcpts, ",")
	for _, r := range []string{partner, senk, boss} {
		if !strings.Contains(rcpts, r) {
			t.Errorf("RCPT TO %v lacks %s", out.rcpts, r)
		}
	}
	if strings.Contains(rcpts, me) || strings.Contains(rcpts, alias) {
		t.Errorf("own address among recipients: %v", out.rcpts)
	}
	if v := headerLine(out.data, "Bcc"); v != "" || strings.Contains(string(out.data), boss) {
		t.Fatalf("Bcc leaked into the transmitted message:\n%s", headerOf(out.data))
	}
	if from := headerLine(out.data, "From"); !strings.Contains(from, "<"+alias+">") {
		t.Errorf("From = %q, want the alias", from)
	}
	if cc := headerLine(out.data, "Cc"); !strings.Contains(cc, senk) || strings.Contains(cc, me) {
		t.Errorf("Cc = %q", cc)
	}
	if v := headerLine(out.data, "In-Reply-To"); v != "<orig-1@partner.test>" {
		t.Errorf("In-Reply-To = %q", v)
	}
	if v := headerLine(out.data, "References"); v != "<root-0@partner.test> <orig-1@partner.test>" {
		t.Errorf("References = %q", v)
	}
	if v := headerLine(out.data, "X-Vimail-Source"); v != "" {
		t.Errorf("internal draft header transmitted: %q", v)
	}
	body := string(out.data)
	if !strings.Contains(body, "PS: edited in the TUI.") || !strings.Contains(body, "Yes, see you there.") || !strings.Contains(body, "> Hi Jakub") {
		t.Errorf("body lost edits or quote:\n%s", body)
	}

	// Server state after the send.
	if n := len(env.imap.messages(t, me, "Drafts")); n != 0 {
		t.Errorf("%d drafts left on the server", n)
	}
	sentCopies := env.imap.messages(t, me, "Sent")
	if len(sentCopies) != 1 || !strings.Contains(headerLine(sentCopies[0].raw, "Bcc"), boss) {
		t.Errorf("Sent copy missing or without Bcc")
	}
	inbox := env.imap.messages(t, me, "INBOX")
	if len(inbox) != 1 || !inbox[0].has(imap.FlagAnswered) {
		t.Errorf("original not flagged \\Answered on the server: %v", inbox[0].flags)
	}
	if len(env.store.Drafts(me)) != 0 {
		t.Errorf("local draft not removed after send")
	}
	if o, _, _ := env.store.MessageByUID(me, "Inbox", orig.UID); !o.Answered {
		t.Errorf("original not answered in the cache")
	}
	for _, op := range env.store.RecentOps(100) {
		if op.Status != cache.OpCompleted {
			t.Errorf("op %s left %s: %s", op.Type, op.Status, op.Error)
		}
	}

	// Delivery: the partner receives it; their vimail threads it.
	env.imap.appendRaw(t, partner, "INBOX", out.data)
	partnerStore := env.partnerSync(t)
	reply := partnerStore.MessagesFor(partner, "Inbox")
	original := partnerStore.MessagesFor(partner, "Sent")
	if len(reply) != 1 || len(original) != 1 {
		t.Fatalf("partner inbox=%d sent=%d", len(reply), len(original))
	}
	if reply[0].InReplyTo != original[0].MessageID || reply[0].ThreadID != original[0].ThreadID || reply[0].ThreadID == "" {
		t.Errorf("reply does not thread: in_reply_to=%q thread=%q, original id=%q thread=%q",
			reply[0].InReplyTo, reply[0].ThreadID, original[0].MessageID, original[0].ThreadID)
	}
	if len(reply[0].Bcc) != 0 || !strings.Contains(reply[0].From, alias) || len(reply[0].Cc) != 1 {
		t.Errorf("received reply from=%q cc=%v bcc=%v", reply[0].From, reply[0].Cc, reply[0].Bcc)
	}
}

// TestServerDraftsReconcile covers drafts written in another client: they
// are imported with every field on sync, and a draft deleted (or sent)
// elsewhere disappears locally, while a draft with unsynced local edits
// stays.
func TestServerDraftsReconcile(t *testing.T) {
	env := newE2E(t)
	foreign := "From: Jakub <jakub@ecomail.test>\r\n" +
		"To: a@example.test, b@example.test\r\n" +
		"Cc: c@example.test\r\n" +
		"Bcc: d@example.test\r\n" +
		"Subject: Written on the phone\r\n" +
		"Message-ID: <phone-1@ecomail.test>\r\n" +
		"In-Reply-To: <x@example.test>\r\n" +
		"References: <x@example.test>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"draft from another client\r\n"
	env.imap.appendRaw(t, me, "Drafts", []byte(foreign), imap.FlagDraft, imap.FlagSeen)
	env.sync(t, me)

	drafts := env.store.Drafts(me)
	if len(drafts) != 1 {
		t.Fatalf("imported %d drafts, want 1", len(drafts))
	}
	d, _ := env.store.Draft(me, drafts[0].ID)
	if len(d.To) != 2 || len(d.Cc) != 1 || len(d.Bcc) != 1 || d.Subject != "Written on the phone" ||
		d.InReplyTo != "x@example.test" || !d.Synced || !strings.Contains(d.Body, "draft from another client") {
		t.Fatalf("imported draft = %+v", d)
	}
	// Syncing again does not import it twice or upload it back.
	env.sync(t, me)
	if n := len(env.store.Drafts(me)); n != 1 || len(env.imap.messages(t, me, "Drafts")) != 1 {
		t.Fatalf("second sync: %d local, %d server drafts", n, len(env.imap.messages(t, me, "Drafts")))
	}

	// A local-only draft that has never uploaded must survive the
	// reconciliation and get uploaded.
	if err := env.store.SaveDraft(me, email.Draft{ID: "local-1", To: []string{"z@example.test"}, Subject: "local"}); err != nil {
		t.Fatal(err)
	}

	// The phone draft is sent from the phone (removed from Drafts).
	c := env.imap.client(t, me)
	if _, err := c.Select("Drafts", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	var all imap.SeqSet
	all.AddNum(1)
	if err := c.Store(all, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // uploaded_at has second precision
	env.sync(t, me)
	left := env.store.Drafts(me)
	if len(left) != 1 || left[0].ID != "local-1" {
		t.Fatalf("after remote delete: %+v", left)
	}
	if got, _ := env.store.Draft(me, "local-1"); !got.Synced {
		t.Fatalf("local draft not uploaded by sync")
	}
	server := env.imap.messages(t, me, "Drafts")
	if len(server) != 1 || headerLine(server[0].raw, "Subject") != "local" {
		t.Fatalf("server drafts after sync = %d", len(server))
	}

	// Editing it replaces the server copy instead of adding a second one.
	if err := env.store.SaveDraft(me, email.Draft{ID: "local-1", To: []string{"z@example.test"}, Subject: "local v2"}); err != nil {
		t.Fatal(err)
	}
	if err := env.coord.FlushDraftOpsNow(me); err != nil {
		t.Fatal(err)
	}
	env.sync(t, me)
	server = env.imap.messages(t, me, "Drafts")
	if len(server) != 1 || headerLine(server[0].raw, "Subject") != "local v2" || len(env.store.Drafts(me)) != 1 {
		t.Fatalf("after edit: %d server drafts (%q), %d local", len(server), headerLine(server[0].raw, "Subject"), len(env.store.Drafts(me)))
	}
}

// partnerSync syncs the correspondent's mailbox with a separate vimail
// install (own config, cache and coordinator) and returns its cache.
func (env *e2eEnv) partnerSync(t *testing.T) *cache.SQLiteStore {
	t.Helper()
	acct := env.cfg.Accounts[0]
	acct.Name, acct.Email, acct.Username, acct.Identities = "Petr Trefil", partner, partner, nil
	cfg := config.DefaultConfig()
	cfg.Accounts = []config.AccountConfig{acct}
	db, err := cache.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := cache.NewSQLiteStore(db)
	if err := store.SeedAccount(acct.Name, acct.Email, acct.IMAPHost, acct.IMAPPort, acct.SMTPHost, acct.SMTPPort); err != nil {
		t.Fatal(err)
	}
	coord := worker.NewCoordinator(cfg, store)
	coord.SetTestCredentials(partner, testCreds(partner))
	t.Cleanup(coord.DisconnectAll)
	if _, err := coord.SyncAccountNow(partner); err != nil {
		t.Fatalf("partner sync: %v", err)
	}
	return store
}
