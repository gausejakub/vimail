package worker_test

import (
	"strings"
	"testing"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/worker"
)

const inviteMessage = "From: Petr Trefil <trefil@partner.test>\r\n" +
	"To: Jakub <jakub@lettr.test>\r\n" +
	"Subject: Invitation: Digifest planning\r\n" +
	"Message-ID: <invite-1@partner.test>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/alternative; boundary=\"b\"\r\n\r\n" +
	"--b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nYou are invited.\r\n" +
	"--b\r\nContent-Type: text/calendar; charset=utf-8; method=REQUEST\r\n\r\n" +
	"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:evt-1@partner.test\r\nSEQUENCE:1\r\n" +
	"DTSTART:20261112T090000Z\r\nDTEND:20261112T100000Z\r\nSUMMARY:Digifest planning\r\nLOCATION:Room 1\r\n" +
	"ORGANIZER;CN=Petr Trefil:mailto:trefil@partner.test\r\n" +
	"ATTENDEE;PARTSTAT=NEEDS-ACTION;CN=Jakub:mailto:jakub@lettr.test\r\n" +
	"ATTENDEE;PARTSTAT=ACCEPTED;CN=Senk:mailto:senk@partner.test\r\n" +
	"END:VEVENT\r\nEND:VCALENDAR\r\n" +
	"--b--\r\n"

// An invitation is shown in the message body, and accepting it sends the
// organizer a calendar REPLY from the address that was invited.
func TestInvitationIsShownAndAnswered(t *testing.T) {
	env := newE2E(t)
	env.imap.appendRaw(t, me, "INBOX", []byte(inviteMessage))
	env.imap.appendRaw(t, me, "INBOX", []byte("From: a@partner.test\r\nTo: "+me+"\r\nSubject: plain\r\nMessage-ID: <p1@partner.test>\r\n\r\nno invitation here\r\n"))
	env.sync(t, me)

	var invite, plain email.Message
	for _, m := range env.store.MessagesFor(me, "Inbox") {
		if strings.HasPrefix(m.Subject, "Invitation") {
			invite = m
		} else {
			plain = m
		}
	}
	body, err := env.coord.PeekBodyNow(me, "Inbox", invite.UID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"── Invitation ──", "Digifest planning", "Thu 12 Nov 2026", "Where:     Room 1",
		"Organizer: Petr Trefil <trefil@partner.test>", "Senk (accepted)", ":accept", "You are invited."} {
		if !strings.Contains(body.Text, want) {
			t.Errorf("body lacks %q:\n%s", want, body.Text)
		}
	}

	res := env.coord.RespondToInvitationNow(me, "Inbox", invite.UID, email.PartAccepted)
	if res.Err != nil || res.Organizer != "trefil@partner.test" || res.Summary != "Digifest planning" {
		t.Fatalf("answer: %+v", res)
	}
	sent := env.smtp.messages()
	if len(sent) != 1 || len(sent[0].rcpts) != 1 || sent[0].rcpts[0] != "trefil@partner.test" {
		t.Fatalf("sent: %+v", sent)
	}
	msg := string(sent[0].data)
	if from := headerLine(sent[0].data, "From"); !strings.Contains(from, alias) {
		t.Errorf("From = %q, want the invited address", from)
	}
	if subject := headerLine(sent[0].data, "Subject"); subject != "Accepted: Digifest planning" {
		t.Errorf("Subject = %q", subject)
	}
	if ct := headerLine(sent[0].data, "Content-Type"); !strings.HasPrefix(ct, "multipart/alternative") {
		t.Errorf("Content-Type = %q", ct)
	}
	cal := worker.ExtractCalendar(sent[0].data)
	reply, ok := email.ParseCalendar(cal)
	if !ok || reply.Method != "REPLY" || reply.UID != "evt-1@partner.test" || reply.Sequence != 1 ||
		len(reply.Attendees) != 1 || reply.Attendees[0].Address != alias || reply.Attendees[0].Status != email.PartAccepted {
		t.Fatalf("calendar reply = %+v\n%s", reply, cal)
	}
	if !strings.Contains(strings.ToLower(msg), "method=reply") {
		t.Errorf("calendar part lacks method=REPLY:\n%s", msg)
	}
	if strings.Contains(string(cal), "senk@partner.test") {
		t.Errorf("other attendees sent back:\n%s", cal)
	}
	// The invitation was read without marking it read, and a copy is in Sent.
	for _, m := range env.imap.messages(t, me, "INBOX") {
		if m.has("\\Seen") {
			t.Errorf("answering marked a message read")
		}
	}
	if n := len(env.imap.messages(t, me, "Sent")); n != 1 {
		t.Errorf("%d messages in Sent, want 1", n)
	}

	// A message without an invitation cannot be answered; nothing is sent.
	if res := env.coord.RespondToInvitationNow(me, "Inbox", plain.UID, email.PartDeclined); res.Err == nil {
		t.Fatal("answered a message without an invitation")
	}
	if n := len(env.smtp.messages()); n != 1 {
		t.Fatalf("%d messages sent, want 1", n)
	}
}
