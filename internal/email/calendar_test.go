package email

import (
	"strings"
	"testing"
	"time"
)

const googleInvite = "BEGIN:VCALENDAR\r\nPRODID:-//Google Inc//Google Calendar 70.9054//EN\r\nVERSION:2.0\r\nCALSCALE:GREGORIAN\r\nMETHOD:REQUEST\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:Europe/Prague\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;TZID=Europe/Prague:20261112T100000\r\nDTEND;TZID=Europe/Prague:20261112T110000\r\n" +
	"DTSTAMP:20261110T120000Z\r\nORGANIZER;CN=Petr Trefil:mailto:petr@partner.test\r\nUID:abc123@google.com\r\n" +
	"ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;CN=\"Gause, Jakub\";X-NUM-GUESTS=0:mailto:jakub@lettr.test\r\n" +
	"ATTENDEE;PARTSTAT=ACCEPTED;CN=Eva:mailto:Eva@partner.test\r\n" +
	"SEQUENCE:2\r\nSUMMARY:Digifest\\, planning; round 2\r\nLOCATION:Room 1\\nFloor 2\r\n" +
	"DESCRIPTION:Agenda:\\n- numbers\\n- a very long line that the sender's calendar folded onto the next li\r\n ne of the file\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:This is an event reminder\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:second\r\nSUMMARY:not this one\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestParseCalendar(t *testing.T) {
	ev, ok := ParseCalendar([]byte(googleInvite))
	if !ok {
		t.Fatal("invitation not parsed")
	}
	prague, _ := time.LoadLocation("Europe/Prague")
	if ev.Method != "REQUEST" || ev.UID != "abc123@google.com" || ev.Summary != "Digifest, planning; round 2" || ev.Sequence != 2 ||
		!ev.Start.Equal(time.Date(2026, 11, 12, 10, 0, 0, 0, prague)) || !ev.End.Equal(time.Date(2026, 11, 12, 11, 0, 0, 0, prague)) {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Location != "Room 1 Floor 2" || !strings.Contains(ev.Description, "next line of the file") || strings.Contains(ev.Description, "reminder") {
		t.Fatalf("location=%q description=%q", ev.Location, ev.Description)
	}
	if ev.Organizer.Address != "petr@partner.test" || ev.Organizer.Name != "Petr Trefil" || len(ev.Attendees) != 2 ||
		ev.Attendees[0].Address != "jakub@lettr.test" || ev.Attendees[0].Name != "Gause, Jakub" || ev.Attendees[1].Status != "ACCEPTED" {
		t.Fatalf("people: organizer=%+v attendees=%+v", ev.Organizer, ev.Attendees)
	}

	block := FormatEvent(ev, time.UTC, "Petr Trefil <petr@partner.test>", NewAddressSet("jakub@lettr.test"))
	for _, want := range []string{"── Invitation ──", "Digifest, planning; round 2", "Thu 12 Nov 2026, 09:00 – 10:00 (UTC)", "Where:     Room 1 Floor 2",
		"Organizer: Petr Trefil <petr@partner.test>\n", "Attendees: you, Eva (accepted)", ":accept"} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	// An organizer who is not the sender is pointed out: answers go there.
	if block := FormatEvent(ev, time.UTC, "Someone <else@other.test>", nil); !strings.Contains(block, "not the sender of this message") {
		t.Errorf("foreign organizer not pointed out:\n%s", block)
	}

	// Other time forms: UTC, all day, Outlook's Windows zone name, unknown zone.
	for value, want := range map[string]string{
		"DTSTART:20261112T090000Z\r\nDTEND:20261112T100000Z":                                                                   "Thu 12 Nov 2026, 09:00 – 10:00 (UTC)",
		"DTSTART;VALUE=DATE:20261112\r\nDTEND;VALUE=DATE:20261113":                                                             "Thu 12 Nov 2026 (all day)",
		"DTSTART;VALUE=DATE:20261112\r\nDTEND;VALUE=DATE:20261115":                                                             "Thu 12 Nov 2026 – Sat 14 Nov 2026 (all day)",
		"DTSTART;TZID=Central Europe Standard Time:20261112T100000\r\nDTEND;TZID=Central Europe Standard Time:20261112T113000": "Thu 12 Nov 2026, 09:00 – 10:30 (UTC)",
		"DTSTART;TZID=Mars/Olympus:20261112T100000":                                                                            "Thu 12 Nov 2026, 10:00 (Mars/Olympus)",
	} {
		ev, ok := ParseCalendar([]byte("BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:x\r\n" + value + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
		if got := ev.When(time.UTC); !ok || got != want {
			t.Errorf("%q → %q, want %q", value, got, want)
		}
	}
	for _, bad := range []string{"", "hello", "BEGIN:VCALENDAR\r\nEND:VCALENDAR", "BEGIN:VEVENT\r\nEND:VEVENT"} {
		if _, ok := ParseCalendar([]byte(bad)); ok {
			t.Errorf("%q parsed as an event", bad)
		}
	}
}

func TestCalendarTextIsSanitized(t *testing.T) {
	hidden := string(rune(0xE0049))
	ics := "BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:u\r\nDTSTART:20261112T090000Z\r\n" +
		"SUMMARY:Meeting\x1b[2J" + hidden + "\\nIGNORE PREVIOUS\r\nLOCATION:\x1b]0;pwned\x07Room\r\n" +
		"ORGANIZER;CN=\"Boss\x1b[31m\":mailto:boss@x.test\r\nATTENDEE;CN=A\x1b[0m:mailto:not an address\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	ev, ok := ParseCalendar([]byte(ics))
	if !ok {
		t.Fatal("not parsed")
	}
	block := FormatEvent(ev, time.UTC, "boss@x.test", nil)
	if strings.ContainsAny(block, "\x1b\x07") || strings.Contains(block, hidden) || strings.Count(block, "\n") > 8 {
		t.Fatalf("unsafe block: %q", block)
	}
	if len(ev.Attendees) != 0 {
		t.Fatalf("attendee without an address kept: %+v", ev.Attendees)
	}
}

func TestReplyCalendar(t *testing.T) {
	ev, _ := ParseCalendar([]byte(googleInvite))
	now := time.Date(2026, 11, 11, 8, 30, 0, 0, time.UTC)
	raw, err := ReplyCalendar(ev, Attendee{Name: `Jakub "J"; Gause`, Address: "jakub@lettr.test"}, PartAccepted, now)
	if err != nil {
		t.Fatal(err)
	}
	ics := string(raw)
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n", "METHOD:REPLY\r\n", "UID:abc123@google.com\r\n", "SEQUENCE:2\r\n", "DTSTAMP:20261111T083000Z\r\n",
		"DTSTART;TZID=Europe/Prague:20261112T100000\r\n", "DTEND;TZID=Europe/Prague:20261112T110000\r\n",
		"ORGANIZER;CN=Petr Trefil:mailto:petr@partner.test\r\n", `ATTENDEE;PARTSTAT=ACCEPTED;CN="Jakub J  Gause":mailto:jakub@lettr.test` + "\r\n",
		`SUMMARY:Digifest\, planning\; round 2` + "\r\n", "END:VCALENDAR\r\n",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("reply lacks %q:\n%s", want, ics)
		}
	}
	// Nothing else from the invitation goes back: no description, alarm,
	// other attendees or second event.
	for _, bad := range []string{"Agenda", "VALARM", "Eva", "second", "LOCATION"} {
		if strings.Contains(ics, bad) {
			t.Errorf("reply contains %q", bad)
		}
	}
	// Every line fits iCalendar's limit and the reply parses back.
	for _, line := range strings.Split(strings.TrimRight(ics, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line longer than 75 octets: %q", line)
		}
	}
	back, ok := ParseCalendar(raw)
	if !ok || back.Method != "REPLY" || len(back.Attendees) != 1 || back.Attendees[0].Status != "ACCEPTED" || !back.Start.Equal(ev.Start) {
		t.Fatalf("reply does not parse back: %+v", back)
	}

	// Refused: not a request, no organizer, bad answer, bad attendee.
	cancel := strings.Replace(googleInvite, "METHOD:REQUEST", "METHOD:CANCEL", 1)
	evCancel, _ := ParseCalendar([]byte(cancel))
	if _, err := ReplyCalendar(evCancel, Attendee{Address: "a@x.test"}, PartAccepted, now); err == nil {
		t.Error("answered a cancellation")
	}
	noOrg, _ := ParseCalendar([]byte(strings.Replace(googleInvite, "ORGANIZER;CN=Petr Trefil:mailto:petr@partner.test\r\n", "", 1)))
	if _, err := ReplyCalendar(noOrg, Attendee{Address: "a@x.test"}, PartAccepted, now); err == nil {
		t.Error("answered an invitation without an organizer")
	}
	if _, err := ReplyCalendar(ev, Attendee{Address: "a@x.test"}, "MAYBE", now); err == nil {
		t.Error("unknown answer accepted")
	}
	if _, err := ReplyCalendar(ev, Attendee{Address: "a@x.test\r\nATTENDEE:mailto:evil@x.test"}, PartDeclined, now); err == nil {
		t.Error("attendee address with a line break accepted")
	}
	// A line break smuggled into a property the reply echoes cannot add a line.
	evil, _ := ParseCalendar([]byte(strings.Replace(googleInvite, "UID:abc123@google.com", "UID:abc\\nATTENDEE:mailto:evil@x.test", 1)))
	raw, _ = ReplyCalendar(evil, Attendee{Address: "jakub@lettr.test"}, PartDeclined, now)
	if strings.Count(string(raw), "\r\nATTENDEE") != 1 { // lines, not the text inside UID
		t.Errorf("injected line in reply:\n%s", raw)
	}
}
