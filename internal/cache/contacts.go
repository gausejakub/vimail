package cache

import (
	"strings"
	"time"

	"github.com/gausejakub/vimail/internal/email"
)

// contactScanLimit bounds how many of the newest messages are read to build
// the contact list; addresses are encrypted per row, so the scan decrypts.
const contactScanLimit = 30000

// Contacts returns correspondents found in cached mail across all accounts,
// best first: recipients of mail in Sent, senders elsewhere. own (and every
// configured account) is left out. Trash and Spam are not read, so a
// spammer's address is never suggested.
func (s *SQLiteStore) Contacts(own []string) []email.Contact {
	set := email.NewAddressSet(own...)
	for _, a := range s.Accounts() {
		set[strings.ToLower(a.Email)] = true
	}
	book := email.NewContactBook(set)

	rows, err := s.db.Query(`
		SELECT f.name, m.from_addr, m.to_addr, m.to_list, m.cc_addr, m.date
		FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE lower(f.name) NOT IN ('trash', 'spam', 'drafts')
		ORDER BY julianday(m.date) DESC LIMIT ?`, contactScanLimit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var folder, from, to, toList, cc, dateStr string
		if rows.Scan(&folder, &from, &to, &toList, &cc, &dateStr) != nil {
			continue
		}
		when, _ := time.Parse(time.RFC3339, dateStr)
		if strings.EqualFold(folder, "Sent") {
			recipients := s.decList(toList)
			if len(recipients) == 0 {
				if legacy := decrypt(s.encKey, to); legacy != "" {
					recipients = []string{legacy}
				}
			}
			for _, r := range append(recipients, s.decList(cc)...) {
				book.Add(r, true, when)
			}
			continue
		}
		book.Add(decrypt(s.encKey, from), false, when)
	}
	return book.Contacts()
}
