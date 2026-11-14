package pgp

import "testing"

func TestParseSignatureStatus(t *testing.T) {
	good := []string{
		"NEWSIG",
		"GOODSIG 1122334455667788 Boss <boss@corp.test>",
		"VALIDSIG SUBKEYFPR 2026-11-14 1700000000 0 4 0 22 8 01 PRIMARYFPR0123456789",
		"TRUST_ULTIMATE 0 pgp",
	}
	sig := parseSignature(good)
	if !sig.Good() || !sig.Trusted || sig.Fingerprint != "PRIMARYFPR0123456789" || sig.Signer != "Boss <boss@corp.test>" {
		t.Fatalf("good: %+v", sig)
	}
	// Two signatures: a trusted key's TRUST line must not vouch for the
	// other key's user id. Reported as an error, not as either signer.
	two := append(append([]string{}, good...), "NEWSIG", "GOODSIG 99AA Lookalike <b0ss@corp.test>", "VALIDSIG X 2026-11-14 1 0 4 0 22 8 01 OTHERFPR")
	if sig := parseSignature(two); sig.Good() || !sig.Multiple || sig.Signer != "" || sig.Trusted {
		t.Fatalf("two signatures: %+v", sig)
	}
	if sig := parseSignature([]string{"NEWSIG", "BADSIG 1122334455667788 Boss <boss@corp.test>"}); sig.Status != "bad" || sig.Good() {
		t.Fatalf("bad: %+v", sig)
	}
	if sig := parseSignature([]string{"ERRSIG 1122334455667788 1 8 00 1700000000 9 -", "NO_PUBKEY 1122334455667788"}); sig.Status != "unknown key" {
		t.Fatalf("unknown key: %+v", sig)
	}
	if sig := parseSignature(nil); sig.Good() {
		t.Fatal("no status lines verified")
	}
}

// Only user ids GnuPG marks valid (full or ultimate) count; one the key's
// owner added without a certification the user trusts does not.
func TestParseUserIDsKeepsOnlyValid(t *testing.T) {
	colons := "pub:f:255:22:AAAA:1700000000:::f:::scESC::::::23::0:\n" +
		"fpr:::::::::AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA:\n" +
		"uid:f::::1700000000::HASH1::Mallory <mallory@corp.test>::::::::::0:\n" +
		"uid:-::::1700000001::HASH2::Alice Smith <alice@corp.test>::::::::::0:\n" +
		"uid:q::::1700000002::HASH3::Bob <bob@corp.test>::::::::::0:\n" +
		"uid:r::::1700000003::HASH4::Old <old@corp.test>::::::::::0:\n" +
		"uid:u::::1700000004::HASH5::Me\\x3a work <ME@X.test>::::::::::0:\n" +
		"uid:f::::1700000005::HASH6::no address here::::::::::0:\n"
	got := parseUserIDs(colons)
	if len(got) != 2 || got[0].Address != "mallory@corp.test" || got[1].Address != "me@x.test" || got[1].Text != "Me: work <ME@X.test>" {
		t.Fatalf("user ids = %+v", got)
	}
	if args := senderArgs("alice@corp.test"); len(args) != 2 || args[0] != "--sender" || args[1] != "alice@corp.test" {
		t.Fatalf("senderArgs = %q", args)
	}
	for _, bad := range []string{"", "--batch", "a b@x.test", "x"} {
		if args := senderArgs(bad); args != nil {
			t.Errorf("senderArgs(%q) = %q", bad, args)
		}
	}
}
