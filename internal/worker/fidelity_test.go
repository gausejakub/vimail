package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/gausejakub/vimail/internal/email"
)

func headerValueOf(raw []byte, name string) string {
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(strings.ReplaceAll(head, "\r\n ", " "), "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func TestComposeMessageRecipientsThreadingAndBcc(t *testing.T) {
	req := SendRequest{
		From:       "Jakub Gause <jakub@lettr.test>",
		ToList:     []string{"Petr Trefil <petr@partner.test>", "eva@partner.test"},
		Cc:         []string{`"Senk, Jan" <senk@partner.test>`, "Žofie <zofie@partner.test>"},
		Bcc:        []string{"boss@hidden.test"},
		Subject:    "Re: Digifest",
		Body:       "hello",
		InReplyTo:  "orig-1@partner.test",
		References: []string{"root-0@partner.test", "orig-1@partner.test", "bad id with spaces"},
	}
	id, wire, err := ComposeMessage(req, ComposeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(id, "@lettr.test") || headerValueOf(wire, "Message-Id") != "<"+id+">" {
		t.Fatalf("message id %q / header %q", id, headerValueOf(wire, "Message-Id"))
	}
	if v := headerValueOf(wire, "Bcc"); v != "" || strings.Contains(string(wire), "boss@hidden.test") {
		t.Fatalf("Bcc in transmitted message: %q", v)
	}
	if v := headerValueOf(wire, "To"); !strings.Contains(v, "petr@partner.test") || !strings.Contains(v, "eva@partner.test") {
		t.Fatalf("To = %q", v)
	}
	if v := headerValueOf(wire, "Cc"); !strings.Contains(v, `"Senk, Jan" <senk@partner.test>`) || !strings.Contains(v, "=?utf-8?") {
		t.Fatalf("Cc = %q (quoted name kept, non-ASCII name encoded)", v)
	}
	if v := headerValueOf(wire, "In-Reply-To"); v != "<orig-1@partner.test>" {
		t.Fatalf("In-Reply-To = %q", v)
	}
	if v := headerValueOf(wire, "References"); v != "<root-0@partner.test> <orig-1@partner.test>" {
		t.Fatalf("References = %q (invalid ids dropped)", v)
	}
	_, keep, err := ComposeMessage(req, ComposeOptions{IncludeBcc: true, MessageID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(headerValueOf(keep, "Bcc"), "boss@hidden.test") || headerValueOf(keep, "Message-Id") != "<"+id+">" {
		t.Fatalf("kept copy: bcc=%q id=%q", headerValueOf(keep, "Bcc"), headerValueOf(keep, "Message-Id"))
	}
	got := req.envelopeRecipients()
	if strings.Join(got, ",") != "petr@partner.test,eva@partner.test,senk@partner.test,zofie@partner.test,boss@hidden.test" {
		t.Fatalf("envelope recipients = %v", got)
	}
}

func TestComposeMessageAttachmentsRoundTrip(t *testing.T) {
	req := SendRequest{
		From: "me@example.test", ToList: []string{"you@example.test"}, Subject: "files", Body: "see attached",
		Attachments: []email.DraftAttachment{
			{Filename: "Digifest 2026 program ř.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4 fake")},
			{Filename: "notes.txt", ContentType: "text/plain", Data: []byte("plain notes")},
		},
	}
	_, raw, err := ComposeMessage(req, ComposeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(headerValueOf(raw, "Content-Type"), "multipart/mixed") {
		t.Fatalf("content type %q", headerValueOf(raw, "Content-Type"))
	}
	parts, err := ExtractAttachmentData(raw)
	if err != nil || len(parts) != 2 {
		t.Fatalf("attachments = %d, %v", len(parts), err)
	}
	if parts[0].Filename != "Digifest 2026 program ř.pdf" || string(parts[0].Data) != "%PDF-1.4 fake" || parts[0].ContentType != "application/pdf" {
		t.Fatalf("attachment 0 = %q %q %q", parts[0].Filename, parts[0].ContentType, parts[0].Data)
	}
	body, _ := ParseBody(raw)
	if !strings.Contains(body.Text, "see attached") {
		t.Fatalf("body = %q", body.Text)
	}
}

func TestExtractFilenameDecodesEncodedNames(t *testing.T) {
	cases := []struct{ disp, ct, want string }{
		// RFC 2047 inside the quoted parameter (what the Digifest PDF used).
		{`attachment; filename="=?UTF-8?Q?Digifest_2026_program_=C5=99.pdf?="`, "application/pdf", "Digifest 2026 program ř.pdf"},
		// Split across encoded words.
		{`attachment; filename="=?UTF-8?Q?Digifest_?= =?UTF-8?Q?2026.pdf?="`, "", "Digifest 2026.pdf"},
		// RFC 2231.
		{`attachment; filename*=UTF-8''p%C5%99%C3%ADloha.pdf`, "", "příloha.pdf"},
		// Only in Content-Type name=.
		{"", `application/pdf; name="=?utf-8?B?xb5sdcWlb3XEjWvDvS5wZGY=?="`, "žluťoučký.pdf"},
		// Malformed header still yields a name.
		{`attachment; filename=report final.pdf; size=1`, "", "report final.pdf"},
		{`attachment; filename="plain.pdf"`, "", "plain.pdf"},
	}
	for _, c := range cases {
		if got := extractFilename(c.disp, c.ct); got != c.want {
			t.Errorf("extractFilename(%q, %q) = %q, want %q", c.disp, c.ct, got, c.want)
		}
	}
}

func TestParseEnvelopeKeepsAllRecipients(t *testing.T) {
	env := &imap.Envelope{
		Subject:   "x",
		From:      []imap.Address{{Name: "Petr", Mailbox: "petr", Host: "partner.test"}},
		ReplyTo:   []imap.Address{{Name: "List", Mailbox: "list", Host: "partner.test"}},
		To:        []imap.Address{{Mailbox: "a", Host: "x.test"}, {Name: "Doe, John", Mailbox: "j", Host: "x.test"}},
		Cc:        []imap.Address{{Mailbox: "c", Host: "x.test"}},
		InReplyTo: []string{"m1@x.test"},
		MessageID: "m2@x.test",
		Date:      time.Now(),
	}
	m := ParseEnvelope(7, env, []imap.Flag{imap.FlagAnswered})
	if len(m.ToList) != 2 || m.ToList[1] != `"Doe, John" <j@x.test>` || len(m.Cc) != 1 || m.ReplyTo[0] != "List <list@partner.test>" ||
		m.InReplyTo != "m1@x.test" || !m.Answered || m.To != `a@x.test, "Doe, John" <j@x.test>` {
		t.Fatalf("message = %+v", m)
	}
	// Servers echo From as Reply-To when the header is absent; drop it.
	env.ReplyTo = env.From
	if m := ParseEnvelope(7, env, nil); m.ReplyTo != nil {
		t.Fatalf("echoed reply-to kept: %q", m.ReplyTo)
	}

	applyExtraHeaders(&m, []byte("References: <m0@x.test>\r\n <m1@x.test>\r\nDelivered-To: Jakub@Lettr.test\r\nX-Original-To: jakub@lettr.test\r\n"))
	if strings.Join(m.References, " ") != "m0@x.test m1@x.test" || len(m.DeliveredTo) != 1 || m.DeliveredTo[0] != "jakub@lettr.test" {
		t.Fatalf("extra headers: refs=%q delivered=%q", m.References, m.DeliveredTo)
	}
}

func TestParseDraftMessageRoundTrip(t *testing.T) {
	src := &email.DraftSource{Kind: email.SourceReply, Folder: "Inbox", UID: 42, MessageID: "orig@x.test"}
	req := SendRequest{
		From: "Jakub <jakub@lettr.test>", ToList: []string{"a@x.test", "B <b@x.test>"}, Cc: []string{"c@x.test"}, Bcc: []string{"d@x.test"},
		Subject: "Re: hi", Body: "text body\nsecond line", InReplyTo: "orig@x.test", References: []string{"root@x.test", "orig@x.test"},
		Attachments: []email.DraftAttachment{{Filename: "a.bin", ContentType: "application/octet-stream", Data: []byte{0, 1, 2}}},
	}
	_, raw, err := ComposeMessage(req, ComposeOptions{IncludeBcc: true, Extra: map[string]string{draftSourceHeader: draftSourceValue(src)}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDraftMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.From != "Jakub <jakub@lettr.test>" || len(d.To) != 2 || d.To[1] != "B <b@x.test>" || d.Cc[0] != "c@x.test" || d.Bcc[0] != "d@x.test" ||
		d.Subject != "Re: hi" || d.Body != "text body\nsecond line" || d.InReplyTo != "orig@x.test" || len(d.References) != 2 ||
		d.Source == nil || *d.Source != *src || len(d.Attachments) != 1 || string(d.Attachments[0].Data) != "\x00\x01\x02" {
		t.Fatalf("draft = %+v", d)
	}
}

// A From that reads as two mailboxes must never be written, whatever the
// checks before it let through.
func TestComposeMessageRefusesMultiMailboxFrom(t *testing.T) {
	for _, from := range []string{
		`x <ceo@corp.test>, <me@x.test>`,
		`ceo@corp.test, me@x.test`,
		`not an address`,
	} {
		if _, raw, err := ComposeMessage(SendRequest{From: from, ToList: []string{"a@x.test"}}, ComposeOptions{}); err == nil {
			t.Errorf("From %q accepted:\n%s", from, raw)
		}
	}
	// A legitimately quoted local part still works and stays one mailbox.
	_, raw, err := ComposeMessage(SendRequest{From: email.FormatAddress("X", `odd local@x.test`), ToList: []string{"a@x.test"}}, ComposeOptions{})
	if err != nil || !strings.Contains(headerValueOf(raw, "From"), `"odd local"@x.test`) {
		t.Fatalf("quoted local part: %v %q", err, headerValueOf(raw, "From"))
	}
}

func TestMediaTypeNormalizesUntrustedValues(t *testing.T) {
	for in, want := range map[string]string{
		"application/pdf; name=x.pdf":         "application/pdf",
		"Application/PDF":                     "application/pdf",
		"message/rfc822":                      "message/rfc822",
		"application/pdf\U000E0041; a=1; a=2": "application/octet-stream", // hidden tag character
		"application/pdf; a=1; a=2":           "application/pdf",          // malformed params, clean type
		"multipart/mixed; boundary=x":         "application/octet-stream",
		"Multipart/Mixed; a=1; a=2":           "application/octet-stream",
		"garbage":                             "application/octet-stream",
		"":                                    "application/octet-stream",
		"text/x\x1b[2J":                       "application/octet-stream",
	} {
		if got := mediaType(in); got != want {
			t.Errorf("mediaType(%q) = %q, want %q", in, got, want)
		}
	}
}
