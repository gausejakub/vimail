package cache

import (
	"strings"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

func TestContactsFromCache(t *testing.T) {
	s := testStore(t)
	key, _ := GenerateEncryptionKey()
	s.SetEncryptionKey(key) // addresses are encrypted at rest
	seedWithFolders(t, s, "Me", "me@x.test")
	for _, f := range []string{"Sent", "Trash", "Spam"} {
		if _, err := s.EnsureFolder("me@x.test", f); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	put := func(folder string, uid uint32, m email.Message) {
		m.UID, m.Date = uid, now.Add(-time.Duration(uid)*time.Hour)
		if err := s.UpsertMessage("me@x.test", folder, m); err != nil {
			t.Fatal(err)
		}
	}
	put("Sent", 1, email.Message{From: "me@x.test", ToList: []string{"Martin <martin@topol.test>", "eva@topol.test"}, Cc: []string{"Senk <senk@topol.test>"}})
	put("Inbox", 2, email.Message{From: "Martin D. <martin@topol.test>", ToList: []string{"me@x.test"}})
	put("Inbox", 3, email.Message{From: "Newsletter <news@shop.test>", ToList: []string{"me@x.test"}})
	put("Spam", 4, email.Message{From: "Prince <prince@scam.test>"})
	put("Trash", 5, email.Message{From: "Deleted <gone@x.test>"})
	put("Inbox", 6, email.Message{From: "Alias <alias@x.test>"})

	got := s.Contacts([]string{"alias@x.test"})
	var addrs []string
	for _, c := range got {
		addrs = append(addrs, c.Address)
	}
	list := strings.Join(addrs, ",")
	if len(got) != 4 || got[0].Address != "martin@topol.test" || got[0].Sent != 1 || got[0].Received != 1 {
		t.Fatalf("contacts = %s (%+v)", list, got)
	}
	for _, bad := range []string{"prince@scam.test", "gone@x.test", "me@x.test", "alias@x.test"} {
		if strings.Contains(list, bad) {
			t.Errorf("%s offered as a contact (%s)", bad, list)
		}
	}
	for _, want := range []string{"eva@topol.test", "senk@topol.test", "news@shop.test"} {
		if !strings.Contains(list, want) {
			t.Errorf("%s missing (%s)", want, list)
		}
	}
}
