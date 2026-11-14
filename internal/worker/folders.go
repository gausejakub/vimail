package worker

import (
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/gausejakub/vimail/internal/email"
)

// Folder roles. vimail addresses special folders by these display names
// ("Sent", "Drafts", "Trash", ...) everywhere: the sidebar, the operation
// queue, the MCP tools. A server folder gets one of them from, in order:
//
//  1. the account's `folders` override in the config,
//  2. the role the server advertises (RFC 6154 SPECIAL-USE),
//  3. its name (FolderName), for servers that advertise nothing.
//
// Roles from 1 and 2 win over names, so a localized or custom-named folder
// (Gmail's "[Gmail]/Gesendet", "Envoyés") is still Sent.

type listedMailbox struct {
	name     string // IMAP mailbox name
	role     string // display name of the advertised role, "" when none
	noSelect bool
}

// roleDisplayName maps a SPECIAL-USE attribute to vimail's display name.
func roleDisplayName(attr imap.MailboxAttr) string {
	switch attr {
	case imap.MailboxAttrSent:
		return "Sent"
	case imap.MailboxAttrDrafts:
		return "Drafts"
	case imap.MailboxAttrTrash:
		return "Trash"
	case imap.MailboxAttrJunk:
		return "Spam"
	case imap.MailboxAttrArchive:
		return "Archive"
	case imap.MailboxAttrAll:
		return "All Mail"
	case imap.MailboxAttrFlagged:
		return "Starred"
	case imap.MailboxAttrImportant:
		return "Important"
	}
	return ""
}

// configRoles are the keys accepted in an account's `folders` table.
var configRoles = map[string]string{
	"sent": "Sent", "drafts": "Drafts", "trash": "Trash", "spam": "Spam", "junk": "Spam",
	"archive": "Archive", "all": "All Mail", "starred": "Starred", "important": "Important",
}

// assignFolderNames returns the display name of every selectable mailbox,
// keyed by IMAP name. Display names are unique: a folder that would take a
// name already given to a role folder keeps its own (sanitized) IMAP name.
func assignFolderNames(listed []listedMailbox, overrides map[string]string) map[string]string {
	display := map[string]string{}
	taken := map[string]bool{}
	give := func(mailbox, name string) bool {
		if taken[name] {
			return false
		}
		display[mailbox], taken[name] = name, true
		return true
	}
	exists := map[string]bool{}
	for _, lm := range listed {
		if !lm.noSelect {
			exists[lm.name] = true
		}
	}
	// 1. Config overrides.
	for key, mailbox := range overrides {
		if role, ok := configRoles[strings.ToLower(strings.TrimSpace(key))]; ok && exists[mailbox] {
			give(mailbox, role)
		}
	}
	// 2. Roles advertised by the server.
	for _, lm := range listed {
		if _, done := display[lm.name]; done || lm.noSelect || lm.role == "" {
			continue
		}
		give(lm.name, lm.role)
	}
	// 3. Names; INBOX first so nothing else can take "Inbox".
	for pass := 0; pass < 2; pass++ {
		for _, lm := range listed {
			if _, done := display[lm.name]; done || lm.noSelect {
				continue
			}
			isInbox := strings.EqualFold(lm.name, "INBOX")
			if (pass == 0) != isInbox {
				continue
			}
			raw := email.SanitizeLine(lm.name)
			if give(lm.name, email.SanitizeLine(FolderName(lm.name))) || give(lm.name, raw) {
				continue
			}
			// Both taken (two folders whose names sanitize alike): keep them
			// apart with a counter rather than merging their mail.
			for n := 2; ; n++ {
				if give(lm.name, raw+" ("+strconv.Itoa(n)+")") {
					break
				}
			}
		}
	}
	return display
}

func displayTaken(display map[string]string, name string) bool {
	for _, d := range display {
		if d == name {
			return true
		}
	}
	return false
}

// listOptions asks for folder roles (RFC 6154) when the server offers them
// through the extended LIST; servers with SPECIAL-USE alone put the role
// attributes into a plain LIST response.
func listOptions(client *imapclient.Client) *imap.ListOptions {
	if caps := client.Caps(); caps.Has(imap.CapSpecialUse) && caps.Has(imap.CapListExtended) {
		return &imap.ListOptions{ReturnSpecialUse: true}
	}
	return nil
}

// listedFrom extracts name, role and selectability from a LIST response.
func listedFrom(mailboxes []*imap.ListData) []listedMailbox {
	var listed []listedMailbox
	for _, mbox := range mailboxes {
		lm := listedMailbox{name: mbox.Mailbox}
		for _, attr := range mbox.Attrs {
			if attr == imap.MailboxAttrNoSelect {
				lm.noSelect = true
			}
			if role := roleDisplayName(attr); role != "" && lm.role == "" {
				lm.role = role
			}
		}
		listed = append(listed, lm)
	}
	return listed
}
