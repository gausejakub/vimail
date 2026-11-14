package worker

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-message/mail"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
)

// maxCalendarPart bounds a calendar part read from a message.
const maxCalendarPart = 512 * 1024

// ExtractCalendar returns the first calendar part of a message (a
// text/calendar part, or an .ics attachment), or nil.
func ExtractCalendar(data []byte) []byte {
	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return findCalendar(mr, 0)
}

func findCalendar(mr *mail.Reader, depth int) []byte {
	for {
		p, err := mr.NextPart()
		if err != nil {
			return nil
		}
		ct := strings.ToLower(p.Header.Get("Content-Type"))
		if strings.HasPrefix(ct, "multipart/") {
			if depth >= maxMIMEDepth {
				continue
			}
			if nested, err := mail.CreateReader(p.Body); err == nil {
				if cal := findCalendar(nested, depth+1); cal != nil {
					return cal
				}
			}
			continue
		}
		name := strings.ToLower(extractFilename(p.Header.Get("Content-Disposition"), p.Header.Get("Content-Type")))
		if strings.HasPrefix(ct, "text/calendar") || strings.HasPrefix(ct, "application/ics") || strings.HasSuffix(name, ".ics") {
			if cal, err := io.ReadAll(io.LimitReader(p.Body, maxCalendarPart)); err == nil && len(cal) > 0 {
				return cal
			}
		}
	}
}

// eventBlock returns the text block describing a message's invitation, or
// "" when it has none.
func eventBlock(data []byte) string {
	cal := ExtractCalendar(data)
	if cal == nil {
		return ""
	}
	ev, ok := email.ParseCalendar(cal)
	if !ok {
		return ""
	}
	from := ""
	if mr, err := mail.CreateReader(bytes.NewReader(data)); err == nil {
		if list, err := mr.Header.AddressList("From"); err == nil && len(list) > 0 {
			from = list[0].Address
		}
		mr.Close()
	}
	return email.FormatEvent(ev, time.Local, from, nil)
}

// InvitationReply is the outcome of answering an invitation.
type InvitationReply struct {
	Organizer string // where the answer went
	Summary   string
	Status    string
	Err       error
}

// RespondToInvitationNow answers the invitation in a message: it sends the
// organizer a calendar REPLY with the user's participation status, from the
// address that was invited. The message is read without marking it read.
func (c *Coordinator) RespondToInvitationNow(acctEmail, folder string, uid uint32, status string) InvitationReply {
	out := InvitationReply{Status: status}
	acct, ok := c.accountByEmail(acctEmail)
	if !ok {
		out.Err = fmt.Errorf("no such account: %s", acctEmail)
		return out
	}
	w, err := c.ensureIMAPWorker(acct)
	if err != nil {
		out.Err = err
		return out
	}
	raw, err := w.PeekRawMessage(folder, uid)
	if err != nil {
		out.Err = err
		return out
	}
	cal := ExtractCalendar(raw)
	ev, ok := email.ParseCalendar(cal)
	if cal == nil || !ok {
		out.Err = fmt.Errorf("this message has no invitation")
		return out
	}
	out.Summary, out.Organizer = ev.Summary, ev.Organizer.Address

	// Answer as the attendee that was invited: one of the account's
	// addresses if it is on the list, otherwise the account address.
	identities := acct.SenderAddresses()
	me := email.Attendee{Name: acct.Name, Address: strings.ToLower(acct.Email)}
	from := identities[0]
	for _, a := range ev.Attendees {
		for _, id := range identities {
			if addr, ok := email.SingleAddress(id); ok && addr == a.Address {
				me.Address, from = a.Address, id
				if a.Name != "" {
					me.Name = a.Name
				}
			}
		}
	}
	ics, err := email.ReplyCalendar(ev, me, status, time.Now())
	if err != nil {
		out.Err = err
		return out
	}
	verb := map[string]string{email.PartAccepted: "Accepted", email.PartTentative: "Tentatively accepted", email.PartDeclined: "Declined"}[status]
	summary := ev.Summary
	if summary == "" {
		summary = "(no title)"
	}
	req := SendRequest{
		From:     from,
		ToList:   []string{email.FormatAddress(ev.Organizer.Name, ev.Organizer.Address)},
		Subject:  verb + ": " + summary,
		Body:     fmt.Sprintf("%s: %s\nWhen: %s\n", verb, summary, ev.When(time.Local)),
		Calendar: &CalendarPart{Method: "REPLY", Data: ics},
	}
	res := c.SendNow(acctEmail, req)
	if res.Err != nil {
		out.Err = res.Err
		return out
	}
	logging.Info("calendar", "invitation answered", logging.Acct(acctEmail), logging.KV("status", status))
	return out
}

// RespondToInvitation is RespondToInvitationNow as a tea.Cmd.
func (c *Coordinator) RespondToInvitation(acctEmail, folder string, uid uint32, status string) tea.Cmd {
	return func() tea.Msg { return c.RespondToInvitationNow(acctEmail, folder, uid, status) }
}
