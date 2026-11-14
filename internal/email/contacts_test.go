package email

import (
	"testing"
	"time"
)

func TestContactBookRanksAndMatches(t *testing.T) {
	own := NewAddressSet("me@x.test", "alias@x.test")
	b := NewContactBook(own)
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }

	b.Add("Martin Dlužanský <martin@topol.test>", true, day(3)) // written to twice
	b.Add("martin@topol.test", true, day(5))
	for i := 0; i < 50; i++ {
		b.Add("Shop <noreply@shop.test>", false, day(10)) // loud, automated
	}
	b.Add(`"Novák, Martina" <martina@firma.test>`, false, day(8))
	b.Add("Žofie <zofie@firma.test>", false, day(9))
	b.Add("me@x.test", true, day(9))                // own address
	b.Add("Alias <ALIAS@x.test>", false, day(9))    // own identity
	b.Add("x <a@x.test>, <b@x.test>", true, day(9)) // two mailboxes: not a contact
	b.Add("garbage", false, day(9))

	all := b.Contacts()
	if len(all) != 4 {
		t.Fatalf("contacts = %+v", all)
	}
	if all[0].Address != "martin@topol.test" || all[0].Sent != 2 || all[0].Name != "Martin Dlužanský" {
		t.Fatalf("first = %+v, want the person written to", all[0])
	}
	if all[len(all)-1].Address != "noreply@shop.test" {
		t.Fatalf("automated sender not last: %+v", all)
	}

	got := MatchContacts(all, "mart", 5)
	if len(got) != 2 || got[0].Address != "martin@topol.test" || got[1].Address != "martina@firma.test" {
		t.Fatalf("mart → %+v", got)
	}
	// Diacritics and case are ignored; word starts inside a name match.
	if got := MatchContacts(all, "ZOF", 5); len(got) != 1 || got[0].Formatted() != "Žofie <zofie@firma.test>" {
		t.Fatalf("ZOF → %+v", got)
	}
	if got := MatchContacts(all, "dluz", 5); len(got) != 1 || got[0].Address != "martin@topol.test" {
		t.Fatalf("dluz → %+v", got)
	}
	// A match inside the address ranks below a prefix match.
	if got := MatchContacts(all, "firma", 5); len(got) != 2 {
		t.Fatalf("firma → %+v", got)
	}
	if got := MatchContacts(all, "m", 1); len(got) != 1 {
		t.Fatalf("limit ignored: %+v", got)
	}
	if got := MatchContacts(all, "  ", 5); got != nil {
		t.Fatalf("empty query matched: %+v", got)
	}
	// The formatted suggestion is a single valid recipient.
	if f := all[1].Formatted(); f == "" {
		t.Fatal("empty formatted contact")
	}
	for _, c := range all {
		if _, ok := SingleAddress(c.Formatted()); !ok {
			t.Errorf("suggestion %q is not one mailbox", c.Formatted())
		}
	}
}
