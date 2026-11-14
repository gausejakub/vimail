package email

import (
	"net/mail"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAddressesSplitsDedupesAndRejects(t *testing.T) {
	got, err := NormalizeAddresses([]string{`a@x.test, "Doe, John" <j@x.test>`, "A@X.test", "Bob <b@y.test>"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.test", `"Doe, John" <j@x.test>`, "Bob <b@y.test>"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The quoted name round-trips instead of splitting into two addresses.
	if again, err := NormalizeAddresses(got); err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("round trip = %q, %v", again, err)
	}
	if _, err := NormalizeAddresses([]string{"not an address"}); err == nil {
		t.Fatal("invalid address accepted")
	}
	if _, err := NormalizeAddresses([]string{"a@x.test\r\nBcc: evil@x.test"}); err == nil {
		t.Fatal("header injection accepted as an address")
	}
}

var own = NewAddressSet("jakub@ecomail.test", "Jakub <jakub@lettr.test>", "jakub@topol.test")

func original() Message {
	return Message{
		From:       "Petr <petr@partner.test>",
		ToList:     []string{"Jakub <jakub@lettr.test>", "Eva <eva@partner.test>"},
		Cc:         []string{"Senk <senk@partner.test>", "jakub@ecomail.test", "JAKUB@TOPOL.TEST"},
		MessageID:  "m2@partner.test",
		InReplyTo:  "m1@partner.test",
		References: []string{"m0@partner.test", "m1@partner.test"},
		Subject:    "Digifest",
		Date:       time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
		Body:       "line one\n> older quote",
	}
}

func TestReplyRecipients(t *testing.T) {
	to, cc := ReplyRecipients(original(), false, own)
	if !reflect.DeepEqual(to, []string{"Petr <petr@partner.test>"}) || cc != nil {
		t.Fatalf("reply: to=%q cc=%q", to, cc)
	}

	// Reply-all: everyone from To and Cc except the user's own addresses
	// (any case, any of their identities), nobody twice.
	to, cc = ReplyRecipients(original(), true, own)
	if !reflect.DeepEqual(to, []string{"Petr <petr@partner.test>"}) {
		t.Fatalf("reply-all to = %q", to)
	}
	if !reflect.DeepEqual(cc, []string{"Eva <eva@partner.test>", "Senk <senk@partner.test>"}) {
		t.Fatalf("reply-all cc = %q", cc)
	}

	// Reply-To wins over From; the sender is not added again.
	m := original()
	m.ReplyTo = []string{"list@partner.test"}
	m.Cc = append(m.Cc, "list@partner.test")
	to, cc = ReplyRecipients(m, true, own)
	if !reflect.DeepEqual(to, []string{"list@partner.test"}) || strings.Contains(strings.Join(cc, ","), "list@") {
		t.Fatalf("reply-to: to=%q cc=%q", to, cc)
	}

	// Replying to one's own sent message goes to its recipients.
	sent := Message{From: "jakub@ecomail.test", ToList: []string{"x@partner.test", "jakub@topol.test"}, Cc: []string{"y@partner.test"}}
	to, cc = ReplyRecipients(sent, true, own)
	if !reflect.DeepEqual(to, []string{"x@partner.test"}) || !reflect.DeepEqual(cc, []string{"y@partner.test"}) {
		t.Fatalf("own message: to=%q cc=%q", to, cc)
	}
}

func TestPickIdentity(t *testing.T) {
	ids := []string{"Jakub <jakub@ecomail.test>", "Jakub Gause <jakub@lettr.test>", "jakub@topol.test"}
	if got := PickIdentity(original(), ids); got != "Jakub Gause <jakub@lettr.test>" {
		t.Fatalf("To alias: %q", got)
	}
	m := Message{ToList: []string{"list@partner.test"}, Cc: []string{"jakub@topol.test"}}
	if got := PickIdentity(m, ids); got != "jakub@topol.test" {
		t.Fatalf("Cc alias: %q", got)
	}
	// Bcc'd or list mail: only Delivered-To names the alias.
	m = Message{ToList: []string{"list@partner.test"}, DeliveredTo: []string{"jakub@lettr.test"}}
	if got := PickIdentity(m, ids); got != "Jakub Gause <jakub@lettr.test>" {
		t.Fatalf("Delivered-To alias: %q", got)
	}
	if got := PickIdentity(Message{ToList: []string{"someone@else.test"}}, ids); got != "" {
		t.Fatalf("no match should mean the primary address, got %q", got)
	}
}

func TestReplyThreadingAndSubjects(t *testing.T) {
	refs := ReplyReferences(original())
	if !reflect.DeepEqual(refs, []string{"m0@partner.test", "m1@partner.test", "m2@partner.test"}) {
		t.Fatalf("references = %q", refs)
	}
	m := original()
	m.References = nil
	if refs := ReplyReferences(m); !reflect.DeepEqual(refs, []string{"m1@partner.test", "m2@partner.test"}) {
		t.Fatalf("references from In-Reply-To = %q", refs)
	}
	m.References = make([]string, 40)
	for i := range m.References {
		m.References[i] = strings.Repeat("r", i+1) + "@x"
	}
	refs = ReplyReferences(m)
	if len(refs) != maxReferences || refs[0] != "r@x" || refs[len(refs)-1] != "m2@partner.test" {
		t.Fatalf("trimmed references keep root and newest: %d %q…%q", len(refs), refs[0], refs[len(refs)-1])
	}
	if ThreadIDFor("m2", "m1", []string{"m0", "m1"}) != "m0" || ThreadIDFor("m2", "m1", nil) != "m1" || ThreadIDFor("m2", "", nil) != "m2" {
		t.Fatal("ThreadIDFor")
	}
	for in, want := range map[string]string{"Digifest": "Re: Digifest", "RE: Digifest": "RE: Digifest", "re:x": "re:x"} {
		if got := ReplySubject(in); got != want {
			t.Errorf("ReplySubject(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"Digifest": "Fwd: Digifest", "FW: x": "FW: x", "Fwd: x": "Fwd: x"} {
		if got := ForwardSubject(in); got != want {
			t.Errorf("ForwardSubject(%q) = %q", in, got)
		}
	}
	q := QuoteReply(original())
	if !strings.Contains(q, "Petr <petr@partner.test> wrote:") || !strings.Contains(q, "> line one\n>> older quote\n") {
		t.Fatalf("quote = %q", q)
	}
}

func TestDecodeHeaderWords(t *testing.T) {
	for in, want := range map[string]string{
		"=?UTF-8?Q?Digifest_2026_program_=C5=99.pdf?=": "Digifest 2026 program ř.pdf",
		"=?utf-8?B?xb5sdcWlb3XEjWvDvS5wZGY=?=":         "žluťoučký.pdf",
		"=?ISO-8859-2?Q?p=F8=EDloha.txt?=":             "příloha.txt",
		"plain.pdf":                                    "plain.pdf",
		"=?broken":                                     "=?broken",
	} {
		if got := DecodeHeaderWords(in); got != want {
			t.Errorf("DecodeHeaderWords(%q) = %q, want %q", in, got, want)
		}
	}
}

// An address whose quoted local part contains ">, <" is one mailbox; it
// must stay one mailbox after formatting, and must not pass as the address
// hidden inside it.
func TestFormatAddressCannotSmuggleASecondMailbox(t *testing.T) {
	got, err := NormalizeAddresses([]string{`x <"ceo@corp.test>, <me"@x.test>`})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %q, %v", got, err)
	}
	list, err := mail.ParseAddressList(got[0])
	if err != nil || len(list) != 1 {
		t.Fatalf("formatted %q reads as %d mailboxes (%v)", got[0], len(list), err)
	}
	if addr, ok := SingleAddress(got[0]); !ok || addr == "me@x.test" || addr == "ceo@corp.test" {
		t.Fatalf("SingleAddress(%q) = %q, %v", got[0], addr, ok)
	}
	if AddrSpec(got[0]) == "me@x.test" {
		t.Fatalf("AddrSpec matched the smuggled mailbox in %q", got[0])
	}
	for _, in := range []string{`a@x.test, b@x.test`, `x <a@x.test>, <b@x.test>`, `nope`, ``} {
		if addr, ok := SingleAddress(in); ok {
			t.Errorf("SingleAddress(%q) = %q, want rejection", in, addr)
		}
	}
	// Every formatted address round-trips as exactly itself.
	for _, c := range []struct{ name, addr string }{
		{"Bob", "b@x.test"}, {"Doe, John", "j@x.test"}, {"", `odd local@x.test`}, {"Ž", `a"b@x.test`},
		{"N", "a@x.test>, <b@y.test"}, {"N", "a@do main<>.test"},
	} {
		f := FormatAddress(c.name, c.addr)
		l, err := mail.ParseAddressList(f)
		if err != nil || len(l) != 1 {
			t.Errorf("FormatAddress(%q, %q) = %q: %d mailboxes, %v", c.name, c.addr, f, len(l), err)
		}
	}
	// A malformed configured identity is never picked or matched.
	if got := PickIdentity(Message{ToList: []string{"me@x.test"}}, []string{`ceo@corp.test>, <me@x.test`, `x <ceo@corp.test>, <me@x.test>`}); got != "" {
		t.Fatalf("PickIdentity picked %q", got)
	}
}
