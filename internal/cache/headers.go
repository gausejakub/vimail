package cache

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gausejakub/vimail/internal/email"
)

// extraHeaderCols are the full-fidelity header columns appended to message
// SELECTs; scan them with extraHeaders.dest and apply with apply.
const extraHeaderCols = `to_list, cc_addr, bcc_addr, reply_to, delivered_to, in_reply_to, refs, thread_id, answered`

type extraHeaders struct {
	toList, cc, bcc, replyTo, deliveredTo string
	inReplyTo, refs, threadID             string
	answered                              int
}

func (e *extraHeaders) dest() []any {
	return []any{&e.toList, &e.cc, &e.bcc, &e.replyTo, &e.deliveredTo, &e.inReplyTo, &e.refs, &e.threadID, &e.answered}
}

func (s *SQLiteStore) apply(e *extraHeaders, m *email.Message) {
	m.ToList = s.decList(e.toList)
	if len(m.ToList) == 0 && m.To != "" {
		// Cached before full headers: the single stored recipient.
		m.ToList = []string{m.To}
	}
	m.Cc = s.decList(e.cc)
	m.Bcc = s.decList(e.bcc)
	m.ReplyTo = s.decList(e.replyTo)
	m.DeliveredTo = s.decList(e.deliveredTo)
	m.InReplyTo = e.inReplyTo
	m.References = plainList(e.refs)
	m.ThreadID = e.threadID
	if m.ThreadID == "" {
		m.ThreadID = email.ThreadIDFor(m.MessageID, m.InReplyTo, m.References)
	}
	m.Answered = e.answered != 0
}

// encList stores an address list as an encrypted JSON array (” when empty).
func (s *SQLiteStore) encList(list []string) string {
	if len(list) == 0 {
		return ""
	}
	data, _ := json.Marshal(list)
	return s.enc(string(data))
}

func (s *SQLiteStore) decList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(decrypt(s.encKey, v)), &out) != nil {
		return nil
	}
	return out
}

// plainList stores Message-ID lists unencrypted, like message_id, so threads
// can be grouped in SQL.
func plainListValue(list []string) string {
	if len(list) == 0 {
		return ""
	}
	data, _ := json.Marshal(list)
	return string(data)
}

func plainList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(v), &out) != nil {
		return nil
	}
	return out
}

// headerArgs returns the values for the extra header columns of msg, in the
// order of headerSetCols.
func (s *SQLiteStore) headerArgs(msg email.Message) []any {
	toList := msg.ToList
	if len(toList) == 0 && msg.To != "" {
		toList = []string{msg.To}
	}
	return []any{
		s.encList(toList), s.encList(msg.Cc), s.encList(msg.Bcc), s.encList(msg.ReplyTo), s.encList(msg.DeliveredTo),
		msg.InReplyTo, plainListValue(msg.References),
		email.ThreadIDFor(msg.MessageID, msg.InReplyTo, msg.References),
		HeadersVersion,
	}
}

// headerSetCols lists the columns headerArgs fills.
const headerSetCols = `to_list, cc_addr, bcc_addr, reply_to, delivered_to, in_reply_to, refs, thread_id, headers_v`

// headerUpdateSet is the ON CONFLICT clause updating the columns above.
const headerUpdateSet = `to_list = excluded.to_list, cc_addr = excluded.cc_addr, bcc_addr = excluded.bcc_addr,
	reply_to = excluded.reply_to, delivered_to = excluded.delivered_to, in_reply_to = excluded.in_reply_to,
	refs = excluded.refs, thread_id = excluded.thread_id, headers_v = excluded.headers_v`

// toDisplay is the To column value: every recipient, comma-joined.
func toDisplay(msg email.Message) string {
	if len(msg.ToList) > 0 {
		return email.JoinAddresses(msg.ToList)
	}
	return msg.To
}

// StaleHeaderUIDs returns up to limit UIDs in a folder whose rows were cached
// before the current header set (HeadersVersion). Sync refetches their
// headers so already-cached mail gains cc, reply-to and threading headers
// without a full resync.
func (s *SQLiteStore) StaleHeaderUIDs(acctEmail, folder string, limit int) []uint32 {
	rows, err := s.db.Query(`SELECT m.uid FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE f.account = ? AND f.name = ? AND m.headers_v < ? AND m.uid < ?
		ORDER BY m.uid DESC LIMIT ?`, acctEmail, folder, HeadersVersion, PlaceholderUIDBase, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var uid uint32
		if rows.Scan(&uid) == nil {
			out = append(out, uid)
		}
	}
	return out
}

// HasStaleHeaders reports whether a folder still has rows to backfill.
func (s *SQLiteStore) HasStaleHeaders(acctEmail, folder string) bool {
	return len(s.StaleHeaderUIDs(acctEmail, folder, 1)) > 0
}

// UpdateHeaders rewrites the header fields of an already-cached message
// (header backfill). Flags, body and attachments are left alone.
func (s *SQLiteStore) UpdateHeaders(acctEmail, folder string, msg email.Message) error {
	var folderID int
	if err := s.db.QueryRow(`SELECT id FROM folders WHERE account = ? AND name = ?`, acctEmail, folder).Scan(&folderID); err != nil {
		return err
	}
	args := []any{msg.MessageID, s.enc(msg.From), s.enc(toDisplay(msg))}
	args = append(args, s.headerArgs(msg)...)
	args = append(args, folderID, msg.UID)
	_, err := s.db.Exec(`UPDATE messages SET
		message_id = CASE WHEN ?1 != '' THEN ?1 ELSE message_id END,
		from_addr = ?2, to_addr = ?3,
		to_list = ?4, cc_addr = ?5, bcc_addr = ?6, reply_to = ?7, delivered_to = ?8,
		in_reply_to = ?9, refs = ?10, thread_id = ?11, headers_v = ?12
		WHERE folder_id = ?13 AND uid = ?14`, args...)
	return err
}

// DecodeAttachmentNames decodes RFC 2047 encoded-words left in attachment
// filenames cached by earlier versions ("=?UTF-8?Q?Digifest_2026...?=").
// It returns how many names changed.
func (s *SQLiteStore) DecodeAttachmentNames() (int, error) {
	rows, err := s.db.Query(`SELECT rowid, filename FROM attachments`)
	if err != nil {
		return 0, err
	}
	type fix struct {
		id   int64
		name string
	}
	var fixes []fix
	for rows.Next() {
		var f fix
		var stored string
		if rows.Scan(&f.id, &stored) != nil {
			continue
		}
		name := decrypt(s.encKey, stored)
		if strings.HasPrefix(stored, encPrefix) && name == stored {
			continue // not decryptable with this key; leave it
		}
		if decoded := email.SanitizeLine(email.DecodeHeaderWords(name)); decoded != name {
			f.name = decoded
			fixes = append(fixes, f)
		}
	}
	rows.Close()
	for _, f := range fixes {
		if _, err := s.db.Exec(`UPDATE attachments SET filename = ? WHERE rowid = ?`, s.enc(f.name), f.id); err != nil {
			return 0, fmt.Errorf("update attachment name: %w", err)
		}
	}
	return len(fixes), nil
}

// SetAnsweredByMessageID sets \Answered on every cached copy of a message
// (Gmail label copies share the flag on the server).
func (s *SQLiteStore) SetAnsweredByMessageID(acctEmail, messageID string) {
	if messageID == "" {
		return
	}
	s.db.Exec(`UPDATE messages SET answered = 1 WHERE message_id = ? AND folder_id IN (SELECT id FROM folders WHERE account = ?)`, messageID, acctEmail)
}

// FolderMessageIDs maps the Message-IDs cached in a folder to their UIDs.
func (s *SQLiteStore) FolderMessageIDs(acctEmail, folder string) map[string]uint32 {
	out := map[string]uint32{}
	rows, err := s.db.Query(`SELECT m.message_id, m.uid FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE f.account = ? AND f.name = ? AND m.message_id != ''`, acctEmail, folder)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var uid uint32
		if rows.Scan(&id, &uid) == nil {
			out[id] = uid
		}
	}
	return out
}

// MarkHeadersCurrent stops header backfill for a row the server no longer has.
func (s *SQLiteStore) MarkHeadersCurrent(acctEmail, folder string, uid uint32) {
	s.db.Exec(`UPDATE messages SET headers_v = ? WHERE uid = ? AND folder_id = (SELECT id FROM folders WHERE account = ? AND name = ?)`,
		HeadersVersion, uid, acctEmail, folder)
}

// likeEscape escapes LIKE wildcards in user input (used with ESCAPE '\').
var likeEscape = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// containsFold reports whether any entry contains the lower-cased query.
func containsFold(list []string, lowerQuery string) bool {
	for _, v := range list {
		if strings.Contains(strings.ToLower(v), lowerQuery) {
			return true
		}
	}
	return false
}
