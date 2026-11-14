package worker

import (
	"strings"
	"time"

	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/email"
)

// SyncRequest requests syncing a specific folder.
type SyncRequest struct {
	Account string
	Folder  string
}

// SyncResult is the result of a folder sync.
type SyncResult struct {
	Account  string
	Folder   string
	NewCount int
	Err      error
}

// SendRequest is a request to send an email via SMTP.
type SendRequest struct {
	From string // header From: the account address or one of its identities
	// To is a comma-separated recipient string (legacy callers); ToList
	// holds one address per entry. Both are sent to.
	To      string
	ToList  []string
	Cc      []string
	Bcc     []string // envelope only; never in the transmitted headers
	Subject string
	Body    string
	// Markdown sends the body with a rendered text/html alternative.
	Markdown bool
	// Sign and Encrypt ask for OpenPGP (PGP/MIME) with the sender's key and
	// the recipients' keys.
	Sign    bool
	Encrypt bool
	// Calendar adds a calendar object as an alternative of the body (the
	// answer to an invitation).
	Calendar    *CalendarPart
	InReplyTo   string
	References  []string
	Attachments []email.DraftAttachment
	// Source is the replied-to or forwarded original; after a successful send
	// it is flagged \Answered or $Forwarded.
	Source *email.DraftSource
	// MessageID and Date pin the message identity across retries.
	MessageID string
	Date      time.Time
}

// allTo returns every To recipient (legacy string and list).
func (r SendRequest) allTo() []string {
	out := append([]string(nil), r.ToList...)
	if strings.TrimSpace(r.To) != "" {
		out = append(out, r.To)
	}
	return out
}

// envelopeRecipients returns the bare address of every recipient (To, Cc,
// Bcc), one RCPT each, without duplicates.
func (r SendRequest) envelopeRecipients() []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range [][]string{r.allTo(), r.Cc, r.Bcc} {
		for _, entry := range group {
			for _, addr := range recipientAddresses(entry) {
				key := strings.ToLower(addr)
				if addr == "" || seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, addr)
			}
		}
	}
	return out
}

// sendPayload converts a request to its queued form.
func (r SendRequest) sendPayload() cache.SendPayload {
	return cache.SendPayload{
		From: r.From, To: r.To, ToList: r.ToList, Cc: r.Cc, Bcc: r.Bcc,
		Subject: r.Subject, Body: r.Body, Markdown: r.Markdown, Sign: r.Sign, Encrypt: r.Encrypt, Calendar: r.Calendar.payload(), InReplyTo: r.InReplyTo, References: r.References,
		Attachments: r.Attachments, Source: r.Source, MessageID: r.MessageID, Date: r.Date,
	}
}

// sendRequestFromPayload restores a queued send.
func sendRequestFromPayload(p cache.SendPayload) SendRequest {
	return SendRequest{
		From: p.From, To: p.To, ToList: p.ToList, Cc: p.Cc, Bcc: p.Bcc,
		Subject: p.Subject, Body: p.Body, Markdown: p.Markdown, Sign: p.Sign, Encrypt: p.Encrypt, Calendar: calendarFromPayload(p.Calendar), InReplyTo: p.InReplyTo, References: p.References,
		Attachments: p.Attachments, Source: p.Source, MessageID: p.MessageID, Date: p.Date,
	}
}

// SendResult is the result of sending an email.
type SendResult struct {
	MessageID string
	Err       error
}

// FetchBodyRequest requests lazy-loading a message body.
type FetchBodyRequest struct {
	Account string
	Folder  string
	UID     uint32
}

// FetchBodyResult is the result of fetching a message body.
type FetchBodyResult struct {
	Account string
	Folder  string
	UID     uint32
	Body    string
	// Security is the outcome of OpenPGP processing ("" for ordinary mail).
	Security    string
	HTMLBody    string
	Attachments []email.Attachment
	Err         error
}

// SyncProgressMsg reports per-folder progress during account sync.
type SyncProgressMsg struct {
	Account  string
	Folder   string
	Done     int // folders completed so far
	Total    int // total folders
	Messages int // messages fetched in current folder (0 = just started)
}

// SyncAccountCompleteMsg signals that a single account finished syncing.
type SyncAccountCompleteMsg struct {
	Account string
	Err     error
}

// SyncAccountLateCompleteMsg reports the result of an account sync that
// outlived the TUI's wait and finished in the background.
type SyncAccountLateCompleteMsg struct {
	Account string
	Err     error
}

// SyncAllCompleteMsg signals that initial sync of all accounts is done.
type SyncAllCompleteMsg struct {
	Errors []error
}

// NewMailMsg signals that new mail arrived via IDLE.
type NewMailMsg struct {
	Account string
	Folder  string
	Count   int
}

// ConnectionStatusMsg reports the connection state of an account.
type ConnectionStatusMsg struct {
	Account   string
	Connected bool
	Err       error
}

// FolderListResult holds the folders discovered for an account.
type FolderListResult struct {
	Account string
	Folders []email.Folder
}

// DeleteProgressMsg reports incremental progress during batch deletion.
type DeleteProgressMsg struct {
	Account string
	Folder  string
	Done    int
	Total   int
}

// DeleteResult is the result of deleting a message via IMAP.
type DeleteResult struct {
	Account string
	Folder  string
	UID     uint32
	Err     error
}

// MarkReadResult reports whether a queued mark-read operation reached IMAP or
// remains durable for retry.
type MarkReadResult struct {
	OpID      int64
	Delivered bool
	Err       error
}

type OperationResult struct {
	OpID      int64
	Delivered bool
	Err       error
}

type VerifyResult struct {
	Account          string
	Folder           string
	Messages         int
	StaleRemoved     int
	MissingAdded     int
	FlagsCorrected   int
	UIDValidityReset bool
	QuickCheckOK     bool
	Orphans          cache.OrphanSweepResult
	Err              error
}

type VerifyCompleteMsg struct{ Results []VerifyResult }

// ExportProgressMsg reports incremental progress during batch export.
type ExportProgressMsg struct {
	Done  int
	Total int
}

// RestoreResult is the result of restoring messages from Trash via IMAP.
type RestoreResult struct {
	Account   string
	DstFolder string
	Count     int
	Remaining []uint32
	OpID      int64
	Delivered bool
	Cached    bool
	Err       error
}

// UIDMove maps a source mailbox UID to the UID assigned in the destination
// mailbox by IMAP MOVE/COPYUID. Destination is zero when the server did not
// report one.
type UIDMove struct {
	Source      uint32
	Destination uint32
}

// CalendarPart is a calendar object sent with a message.
type CalendarPart struct {
	Method string // iTIP method, e.g. "REPLY"
	Data   []byte
}

func (c *CalendarPart) payload() *cache.CalendarPayload {
	if c == nil {
		return nil
	}
	return &cache.CalendarPayload{Method: c.Method, Data: c.Data}
}

func calendarFromPayload(p *cache.CalendarPayload) *CalendarPart {
	if p == nil {
		return nil
	}
	return &CalendarPart{Method: p.Method, Data: p.Data}
}
