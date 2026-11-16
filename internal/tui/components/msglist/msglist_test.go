package msglist

import "testing"

// A display name that is another address must not stand in for the sender.
func TestDisplayNameDoesNotShowAForeignAddressAsName(t *testing.T) {
	for in, want := range map[string]string{
		`"ceo@corp.com" <mallory@evil.test>`:    "mallory@evil.test",
		`ceo@corp.com <mallory@evil.test>`:      "mallory@evil.test",
		`"Bob (bob@corp.com)" <mallory@x.test>`: "mallory@x.test",
		`"bob@corp.com" <bob@corp.com>`:         `"bob@corp.com"`,
		`Bob Smith <bob@corp.com>`:              "Bob Smith",
		`bob@corp.com`:                          "bob@corp.com",
	} {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}
