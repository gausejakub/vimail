package email

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Calendar invitations (iCalendar, RFC 5545; scheduling, RFC 5546).
//
// An invitation arrives as a text/calendar part. Everything in it is
// written by the sender: it is parsed defensively, shown sanitized, and
// only a fixed set of properties is ever echoed back in a reply.

// Event is the first event of a calendar object.
type Event struct {
	Method      string // REQUEST, CANCEL, REPLY, PUBLISH ("" when absent)
	UID         string
	Summary     string
	Location    string
	Description string
	Start, End  time.Time
	AllDay      bool
	// Zone names the time zone the times were given in, when it could not
	// be resolved (Start and End are then wall-clock times in UTC).
	Zone      string
	Organizer Attendee
	Attendees []Attendee
	Sequence  int
	Recurring bool

	// Raw property lines (unfolded), kept for building a reply.
	rawStart, rawEnd, rawRecurrenceID, rawOrganizer string
}

// Attendee is a participant of an event.
type Attendee struct {
	Name    string
	Address string // lower-cased, without "mailto:"
	// Status is the participation status: NEEDS-ACTION, ACCEPTED,
	// TENTATIVE, DECLINED ("" when absent).
	Status string
}

// Participation answers for ReplyCalendar.
const (
	PartAccepted  = "ACCEPTED"
	PartTentative = "TENTATIVE"
	PartDeclined  = "DECLINED"
)

// maxCalendarBytes bounds what is parsed of a calendar part.
const maxCalendarBytes = 512 * 1024

type icalProp struct {
	name   string
	params map[string]string
	value  string
	raw    string
}

// parseICalLines unfolds and splits a calendar object into properties.
func parseICalLines(data []byte) []icalProp {
	if len(data) > maxCalendarBytes {
		data = data[:maxCalendarBytes]
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// Unfold: a line starting with a space or tab continues the previous one.
	text = strings.ReplaceAll(text, "\n ", "")
	text = strings.ReplaceAll(text, "\n\t", "")
	var props []icalProp
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		// The name and parameters end at the first colon outside quotes.
		inQuote, colon := false, -1
		for i, r := range line {
			if r == '"' {
				inQuote = !inQuote
			} else if r == ':' && !inQuote {
				colon = i
				break
			}
		}
		if colon < 0 {
			continue
		}
		head, value := line[:colon], line[colon+1:]
		parts := splitOutsideQuotes(head, ';')
		p := icalProp{name: strings.ToUpper(strings.TrimSpace(parts[0])), params: map[string]string{}, value: value, raw: line}
		for _, param := range parts[1:] {
			if k, v, ok := strings.Cut(param, "="); ok {
				p.params[strings.ToUpper(strings.TrimSpace(k))] = strings.Trim(v, `"`)
			}
		}
		props = append(props, p)
	}
	return props
}

func splitOutsideQuotes(s string, sep rune) []string {
	var out []string
	inQuote, start := false, 0
	for i, r := range s {
		if r == '"' {
			inQuote = !inQuote
		} else if r == sep && !inQuote {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// icalText undoes the escaping of a TEXT value and makes it one safe line
// (or, for descriptions, safe multi-line text).
func icalText(v string, multiline bool) string {
	v = strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(v)
	if multiline {
		return strings.TrimSpace(SanitizeText(v))
	}
	return strings.TrimSpace(SanitizeLine(v))
}

// ParseCalendar reads the first event of a calendar object. It reports
// false when the data is not a calendar with an event.
func ParseCalendar(data []byte) (Event, bool) {
	var ev Event
	inEvent, seenEvent, inOther := false, false, 0
	for _, p := range parseICalLines(data) {
		switch {
		case p.name == "BEGIN" && strings.EqualFold(p.value, "VEVENT"):
			if seenEvent {
				return ev, ev.UID != "" || !ev.Start.IsZero() // only the first event
			}
			inEvent, seenEvent = true, true
			continue
		case p.name == "END" && strings.EqualFold(p.value, "VEVENT"):
			inEvent = false
			continue
		case p.name == "BEGIN" && inEvent: // VALARM and the like inside the event
			inOther++
			continue
		case p.name == "END" && inEvent && inOther > 0:
			inOther--
			continue
		}
		if !inEvent {
			if p.name == "METHOD" {
				ev.Method = strings.ToUpper(icalText(p.value, false))
			}
			continue
		}
		if inOther > 0 {
			continue
		}
		switch p.name {
		case "UID":
			ev.UID = icalText(p.value, false)
		case "SUMMARY":
			ev.Summary = icalText(p.value, false)
		case "LOCATION":
			ev.Location = icalText(p.value, false)
		case "DESCRIPTION":
			ev.Description = icalText(p.value, true)
		case "SEQUENCE":
			ev.Sequence, _ = strconv.Atoi(strings.TrimSpace(p.value))
		case "RRULE":
			ev.Recurring = true
		case "RECURRENCE-ID":
			ev.rawRecurrenceID = p.raw
		case "DTSTART":
			var zone string
			ev.Start, ev.AllDay, zone = icalTime(p)
			if zone != "" {
				ev.Zone = zone
			}
			ev.rawStart = p.raw
		case "DTEND":
			ev.End, _, _ = icalTime(p)
			ev.rawEnd = p.raw
		case "ORGANIZER":
			ev.Organizer = icalAttendee(p)
			ev.rawOrganizer = p.raw
		case "ATTENDEE":
			if a := icalAttendee(p); a.Address != "" {
				ev.Attendees = append(ev.Attendees, a)
			}
		}
	}
	return ev, seenEvent && (ev.UID != "" || !ev.Start.IsZero())
}

func icalAttendee(p icalProp) Attendee {
	addr := strings.TrimSpace(p.value)
	if len(addr) >= 7 && strings.EqualFold(addr[:7], "mailto:") {
		addr = addr[7:]
	}
	a := Attendee{Name: icalText(p.params["CN"], false), Status: strings.ToUpper(SanitizeLine(p.params["PARTSTAT"]))}
	if single, ok := SingleAddress(addr); ok {
		a.Address = single
	}
	return a
}

// windowsZones maps the Windows time zone names Outlook writes into
// invitations to IANA names, for the zones seen most.
var windowsZones = map[string]string{
	"UTC": "UTC", "GMT Standard Time": "Europe/London", "W. Europe Standard Time": "Europe/Berlin",
	"Central Europe Standard Time": "Europe/Prague", "Central European Standard Time": "Europe/Warsaw",
	"Romance Standard Time": "Europe/Paris", "E. Europe Standard Time": "Europe/Chisinau",
	"FLE Standard Time": "Europe/Kiev", "GTB Standard Time": "Europe/Bucharest",
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "Pacific Standard Time": "America/Los_Angeles",
	"India Standard Time": "Asia/Kolkata", "Tokyo Standard Time": "Asia/Tokyo",
	"AUS Eastern Standard Time": "Australia/Sydney",
}

// icalTime parses DTSTART/DTEND. unresolved is the TZID when it names a
// zone this system does not know; the time is then kept as wall-clock UTC.
func icalTime(p icalProp) (t time.Time, allDay bool, unresolved string) {
	v := strings.TrimSpace(p.value)
	if strings.EqualFold(p.params["VALUE"], "DATE") || len(v) == 8 {
		t, err := time.Parse("20060102", v)
		if err != nil {
			return time.Time{}, false, ""
		}
		return t, true, ""
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		if err != nil {
			return time.Time{}, false, ""
		}
		return t, false, ""
	}
	loc := time.Local // "floating" time: the reader's own zone
	if tzid := p.params["TZID"]; tzid != "" {
		name := tzid
		if mapped, ok := windowsZones[tzid]; ok {
			name = mapped
		}
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		} else {
			loc, unresolved = time.UTC, SanitizeLine(tzid)
		}
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	if err != nil {
		return time.Time{}, false, ""
	}
	return t, false, unresolved
}

// When describes the event's time for the reader, in loc.
func (e Event) When(loc *time.Location) string {
	if e.Start.IsZero() {
		return "time not given"
	}
	if e.AllDay {
		// DTEND of an all-day event is the day after the last day.
		last := e.End.AddDate(0, 0, -1)
		if e.End.IsZero() || !last.After(e.Start) {
			return e.Start.Format("Mon 2 Jan 2006") + " (all day)"
		}
		return e.Start.Format("Mon 2 Jan 2006") + " – " + last.Format("Mon 2 Jan 2006") + " (all day)"
	}
	if e.Zone != "" {
		// The zone is unknown here: show the time as the sender wrote it.
		s := e.Start.Format("Mon 2 Jan 2006, 15:04")
		if !e.End.IsZero() {
			s += " – " + e.End.Format("15:04")
		}
		return s + " (" + e.Zone + ")"
	}
	start := e.Start.In(loc)
	s := start.Format("Mon 2 Jan 2006, 15:04")
	if !e.End.IsZero() {
		end := e.End.In(loc)
		if end.Format("20060102") == start.Format("20060102") {
			s += " – " + end.Format("15:04")
		} else {
			s += " – " + end.Format("Mon 2 Jan 2006, 15:04")
		}
	}
	return s + " (" + start.Format("MST") + ")"
}

// FormatEvent renders an event as a text block for the top of a message
// body. from is the message's sender, to point out an organizer that is
// someone else; own marks the reader among the attendees.
func FormatEvent(e Event, loc *time.Location, from string, own AddressSet) string {
	title := "Invitation"
	switch e.Method {
	case "CANCEL":
		title = "Cancelled event"
	case "REPLY":
		title = "Reply to an invitation"
	case "PUBLISH", "":
		title = "Calendar event"
	}
	var b strings.Builder
	b.WriteString("── " + title + " ──\n")
	summary := e.Summary
	if summary == "" {
		summary = "(no title)"
	}
	b.WriteString("  " + summary + "\n")
	when := e.When(loc)
	if e.Recurring {
		when += ", repeats"
	}
	b.WriteString("  When:      " + when + "\n")
	if e.Location != "" {
		b.WriteString("  Where:     " + e.Location + "\n")
	}
	if e.Organizer.Address != "" {
		org := FormatAddress(e.Organizer.Name, e.Organizer.Address)
		if from != "" && AddrSpec(from) != e.Organizer.Address {
			org += "  (not the sender of this message)"
		}
		b.WriteString("  Organizer: " + org + "\n")
	}
	if len(e.Attendees) > 0 {
		var names []string
		for i, a := range e.Attendees {
			if i == 8 {
				names = append(names, fmt.Sprintf("and %d more", len(e.Attendees)-8))
				break
			}
			name := a.Name
			if name == "" {
				name = a.Address
			}
			if own[a.Address] {
				name = "you"
			}
			switch a.Status {
			case PartAccepted:
				name += " (accepted)"
			case PartDeclined:
				name += " (declined)"
			case PartTentative:
				name += " (maybe)"
			}
			names = append(names, name)
		}
		b.WriteString("  Attendees: " + strings.Join(names, ", ") + "\n")
	}
	if e.Method == "REQUEST" && e.Organizer.Address != "" {
		b.WriteString("  Answer with :accept, :tentative or :decline\n")
	}
	b.WriteString("──\n")
	return b.String()
}

// ReplyCalendar builds the iTIP REPLY that tells the organizer the
// attendee's answer. Only the properties a reply needs are taken from the
// invitation, each as one re-folded line, so nothing else the sender put in
// the invitation is sent back.
func ReplyCalendar(e Event, attendee Attendee, status string, now time.Time) ([]byte, error) {
	if e.Method != "REQUEST" {
		return nil, fmt.Errorf("this is not an invitation that asks for an answer")
	}
	if e.UID == "" || e.rawOrganizer == "" || e.Organizer.Address == "" {
		return nil, fmt.Errorf("the invitation names no organizer to answer")
	}
	switch status {
	case PartAccepted, PartTentative, PartDeclined:
	default:
		return nil, fmt.Errorf("unknown answer %q", status)
	}
	if _, ok := SingleAddress(attendee.Address); !ok {
		return nil, fmt.Errorf("no attendee address")
	}
	var lines []string
	add := func(line string) {
		if line = SanitizeLine(line); line != "" {
			lines = append(lines, line)
		}
	}
	add("BEGIN:VCALENDAR")
	add("PRODID:-//vimail//vimail//EN")
	add("VERSION:2.0")
	add("METHOD:REPLY")
	add("BEGIN:VEVENT")
	add("UID:" + icalEscape(e.UID))
	add("SEQUENCE:" + strconv.Itoa(e.Sequence))
	add("DTSTAMP:" + now.UTC().Format("20060102T150405Z"))
	add(e.rawStart)
	add(e.rawEnd)
	add(e.rawRecurrenceID)
	add(e.rawOrganizer)
	who := "ATTENDEE;PARTSTAT=" + status
	if name := strings.NewReplacer(`"`, "", ";", " ", ":", " ", ",", " ").Replace(SanitizeLine(attendee.Name)); strings.TrimSpace(name) != "" {
		who += `;CN="` + strings.TrimSpace(name) + `"`
	}
	add(who + ":mailto:" + attendee.Address)
	if e.Summary != "" {
		add("SUMMARY:" + icalEscape(e.Summary))
	}
	add("END:VEVENT")
	add("END:VCALENDAR")

	var b strings.Builder
	for _, line := range lines {
		b.WriteString(foldICal(line))
		b.WriteString("\r\n")
	}
	return []byte(b.String()), nil
}

func icalEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace(SanitizeLine(s))
}

// foldICal folds a content line at 75 octets, on a character boundary.
func foldICal(line string) string {
	const limit = 75
	if len(line) <= limit {
		return line
	}
	var b strings.Builder
	width := 0
	for _, r := range line {
		n := len(string(r))
		if width+n > limit {
			b.WriteString("\r\n ")
			width = 1
		}
		b.WriteRune(r)
		width += n
	}
	return b.String()
}
