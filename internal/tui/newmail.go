package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
	"github.com/gausejakub/vimail/internal/notify"
)

// newMailScan is how many of the newest messages of a folder are looked at.
const newMailScan = 50

// sendNotification shows a notification; tests replace it.
var sendNotification = notify.Send

// newMailCmd looks for unread messages that arrived since the last look in
// the watched folders and shows one notification per account. The first
// look at a folder only records where it stands, so mail that was already
// there when vimail started does not notify.
func (m Model) newMailCmd() tea.Cmd {
	cfg := m.cfg.Notify
	if !cfg.Enabled || m.mailSeen == nil {
		return nil
	}
	type batch struct {
		account string
		msgs    []email.Message
	}
	var batches []batch
	for _, acct := range m.store.Accounts() {
		var fresh []email.Message
		for _, folder := range cfg.WatchedFolders() {
			if !cfg.Watches(acct.Email, folder) {
				continue
			}
			key := acct.Email + "\x00" + folder
			seen, known := m.mailSeen[key]
			highest := seen
			for _, msg := range m.store.MessagesForPage(acct.Email, folder, 0, newMailScan) {
				// Placeholder UIDs belong to moves still on their way to the
				// server, not to new mail.
				if msg.UID == 0 || msg.UID >= cache.PlaceholderUIDBase {
					continue
				}
				if msg.UID > highest {
					highest = msg.UID
				}
				if known && msg.UID > seen && msg.Unread {
					fresh = append(fresh, msg)
				}
			}
			m.mailSeen[key] = highest
		}
		if len(fresh) > 0 {
			batches = append(batches, batch{account: acct.Email, msgs: fresh})
		}
	}
	if len(batches) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, b := range batches {
			title, body := notify.Format(b.account, b.msgs, !cfg.HideSubject)
			if err := sendNotification(cfg, title, body); err != nil {
				logging.Warn("notify", "notification failed", logging.Acct(b.account), logging.Err(err))
			}
		}
		return nil
	}
}
