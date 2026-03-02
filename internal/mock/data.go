package mock

import (
	"fmt"
	"time"
)

type Account struct {
	Name  string
	Email string
}

type Folder struct {
	Name        string
	UnreadCount int
}

type Message struct {
	ID      string
	From    string
	To      string
	Subject string
	Body    string
	Date    time.Time
	Unread  bool
	Flagged bool
}

var Accounts = []Account{
	{Name: "Personal", Email: "alice@example.com"},
	{Name: "Work", Email: "alice@acme.corp"},
	{Name: "School", Email: "alice@university.edu"},
	{Name: "Side Project", Email: "alice@myapp.dev"},
}

var folders = map[string][]Folder{
	"alice@example.com": {
		{Name: "Inbox", UnreadCount: 3},
		{Name: "Sent", UnreadCount: 0},
		{Name: "Drafts", UnreadCount: 1},
		{Name: "Trash", UnreadCount: 0},
	},
	"alice@acme.corp": {
		{Name: "Inbox", UnreadCount: 2},
		{Name: "Sent", UnreadCount: 0},
		{Name: "Drafts", UnreadCount: 0},
		{Name: "Trash", UnreadCount: 0},
	},
	"alice@university.edu": {
		{Name: "Inbox", UnreadCount: 2},
		{Name: "Sent", UnreadCount: 0},
		{Name: "Drafts", UnreadCount: 1},
		{Name: "Trash", UnreadCount: 0},
	},
	"alice@myapp.dev": {
		{Name: "Inbox", UnreadCount: 1},
		{Name: "Sent", UnreadCount: 0},
		{Name: "Drafts", UnreadCount: 0},
		{Name: "Trash", UnreadCount: 0},
	},
}

var messages = map[string]map[string][]Message{
	"alice@example.com": {
		"Inbox": {
			{
				ID: "p1", From: "Bob <bob@example.com>", To: "alice@example.com",
				Subject: "Weekend plans?", Body: "Hey Alice,\n\nAre you free this Saturday? I was thinking we could grab coffee and catch up.\n\nLet me know!\n\nBob",
				Date: time.Now().Add(-10 * time.Minute), Unread: true,
			},
			{
				ID: "p2", From: "Newsletter <news@tech.io>", To: "alice@example.com",
				Subject: "This Week in Go", Body: "Go 1.24 has landed with some exciting changes:\n\n- Generic type aliases\n- Improved toolchain management\n- New testing/synctest package\n\nRead more at go.dev/blog",
				Date: time.Now().Add(-2 * time.Hour), Unread: true,
			},
			{
				ID: "p3", From: "GitHub <noreply@github.com>", To: "alice@example.com",
				Subject: "[vmail] New issue: Add theme support", Body: "A new issue has been opened in gause/vmail:\n\n#42 Add theme support\n\nOpened by @contributor\n\nWe should support multiple color themes including tokyonight, catppuccin, and gruvbox.",
				Date: time.Now().Add(-1 * time.Hour), Unread: true, Flagged: true,
			},
			{
				ID: "p4", From: "Mom <mom@example.com>", To: "alice@example.com",
				Subject: "Re: Dinner on Sunday", Body: "Sounds wonderful! I'll make your favorite lasagna. See you at 6pm.\n\nLove,\nMom",
				Date: time.Now().Add(-26 * time.Hour), Unread: false,
			},
			{
				ID: "p5", From: "Stripe <receipts@stripe.com>", To: "alice@example.com",
				Subject: "Your receipt from Acme Inc", Body: "Amount: $9.99\nDescription: Monthly subscription\nDate: March 1, 2026\n\nThank you for your purchase.",
				Date: time.Now().Add(-50 * time.Hour), Unread: false, Flagged: true,
			},
		},
		"Sent": {
			{
				ID: "p6", From: "alice@example.com", To: "bob@example.com",
				Subject: "Re: Weekend plans?", Body: "Hey Bob,\n\nSaturday works! Let's meet at the usual place at 2pm.\n\nAlice",
				Date: time.Now().Add(-5 * time.Minute), Unread: false,
			},
		},
		"Drafts": {
			{
				ID: "p7", From: "alice@example.com", To: "",
				Subject: "Blog post draft", Body: "Title: Building a TUI Email Client in Go\n\n[Draft in progress...]",
				Date: time.Now().Add(-3 * time.Hour), Unread: false,
			},
		},
		"Trash": {},
	},
	"alice@acme.corp": {
		"Inbox": {
			{
				ID: "w1", From: "Charlie <charlie@acme.corp>", To: "alice@acme.corp",
				Subject: "Q1 Planning meeting", Body: "Hi team,\n\nPlease review the attached agenda before tomorrow's Q1 planning meeting at 10am.\n\nThanks,\nCharlie",
				Date: time.Now().Add(-30 * time.Minute), Unread: true,
			},
			{
				ID: "w2", From: "HR <hr@acme.corp>", To: "alice@acme.corp",
				Subject: "Updated PTO policy", Body: "Dear team,\n\nPlease find the updated PTO policy effective immediately. Key changes:\n\n- Unlimited PTO for all full-time employees\n- Minimum 15 days encouraged\n- Manager approval still required for >5 consecutive days\n\nBest,\nHR Team",
				Date: time.Now().Add(-4 * time.Hour), Unread: true,
			},
			{
				ID: "w3", From: "Jira <jira@acme.corp>", To: "alice@acme.corp",
				Subject: "[PROJ-123] Bug: Login timeout", Body: "Issue PROJ-123 has been assigned to you.\n\nPriority: High\nReporter: Dave\nDescription: Users experience timeout errors when logging in during peak hours.",
				Date: time.Now().Add(-8 * time.Hour), Unread: false, Flagged: true,
			},
			{
				ID: "w4", From: "Dave <dave@acme.corp>", To: "alice@acme.corp",
				Subject: "Re: API design review", Body: "Alice,\n\nLooks good overall. A few suggestions:\n\n1. Use PATCH instead of PUT for partial updates\n2. Add pagination to the list endpoints\n3. Consider rate limiting\n\nLet's discuss tomorrow.\n\nDave",
				Date: time.Now().Add(-25 * time.Hour), Unread: false,
			},
			{
				ID: "w5", From: "CI/CD <ci@acme.corp>", To: "alice@acme.corp",
				Subject: "Build #456 passed", Body: "Pipeline: main\nCommit: abc1234\nStatus: All checks passed\nDuration: 3m 42s",
				Date: time.Now().Add(-48 * time.Hour), Unread: false,
			},
		},
		"Sent":   {},
		"Drafts": {},
		"Trash":  {},
	},
	"alice@university.edu": {
		"Inbox": {
			{
				ID: "s1", From: "Prof. Smith <smith@university.edu>", To: "alice@university.edu",
				Subject: "Midterm grades posted", Body: "Hi Alice,\n\nYour midterm grades have been posted to the student portal. Please review and let me know if you have any questions.\n\nBest,\nProf. Smith",
				Date: time.Now().Add(-3 * time.Hour), Unread: true,
			},
			{
				ID: "s2", From: "Study Group <studygroup@university.edu>", To: "alice@university.edu",
				Subject: "Meeting tomorrow at 4pm", Body: "Hey everyone,\n\nReminder that we're meeting in the library tomorrow at 4pm to review chapter 7.\n\nBring your notes!",
				Date: time.Now().Add(-6 * time.Hour), Unread: true,
			},
			{
				ID: "s3", From: "Library <library@university.edu>", To: "alice@university.edu",
				Subject: "Book due in 3 days", Body: "This is a reminder that the following book is due soon:\n\nTitle: Introduction to Algorithms\nDue: March 5, 2026\n\nPlease return or renew online.",
				Date: time.Now().Add(-24 * time.Hour), Unread: false,
			},
		},
		"Sent": {},
		"Drafts": {
			{
				ID: "s4", From: "alice@university.edu", To: "smith@university.edu",
				Subject: "Re: Office hours question", Body: "Dear Prof. Smith,\n\n[Draft in progress...]",
				Date: time.Now().Add(-12 * time.Hour), Unread: false,
			},
		},
		"Trash": {},
	},
	"alice@myapp.dev": {
		"Inbox": {
			{
				ID: "d1", From: "Vercel <notifications@vercel.com>", To: "alice@myapp.dev",
				Subject: "Deployment successful", Body: "Your project myapp has been deployed successfully.\n\nURL: https://myapp.dev\nCommit: feat: add user dashboard\nDuration: 45s",
				Date: time.Now().Add(-1 * time.Hour), Unread: true,
			},
			{
				ID: "d2", From: "GitHub <noreply@github.com>", To: "alice@myapp.dev",
				Subject: "[myapp] PR #12: Fix auth redirect", Body: "Pull request #12 has been opened by @contributor:\n\nFix authentication redirect loop when session expires.\n\n+15 -3 files changed",
				Date: time.Now().Add(-5 * time.Hour), Unread: false, Flagged: true,
			},
		},
		"Sent":   {},
		"Drafts": {},
		"Trash":  {},
	},
}

func SaveDraft(email string, msg Message) {
	acct, ok := messages[email]
	if !ok {
		return
	}
	drafts := acct["Drafts"]

	// Update existing draft if ID matches
	for i, d := range drafts {
		if d.ID == msg.ID {
			drafts[i] = msg
			acct["Drafts"] = drafts
			return
		}
	}

	// New draft
	acct["Drafts"] = append(drafts, msg)

	// Update unread count in folders
	if fs, ok := folders[email]; ok {
		for i, f := range fs {
			if f.Name == "Drafts" {
				fs[i].UnreadCount = len(acct["Drafts"])
				break
			}
		}
	}
}

func DeleteDraft(email, id string) {
	acct, ok := messages[email]
	if !ok {
		return
	}
	drafts := acct["Drafts"]
	for i, d := range drafts {
		if d.ID == id {
			acct["Drafts"] = append(drafts[:i], drafts[i+1:]...)
			break
		}
	}

	// Update unread count in folders
	if fs, ok := folders[email]; ok {
		for i, f := range fs {
			if f.Name == "Drafts" {
				fs[i].UnreadCount = len(acct["Drafts"])
				break
			}
		}
	}
}

var draftSeq int

func NextDraftID() string {
	draftSeq++
	return fmt.Sprintf("draft-%d", draftSeq)
}

func FoldersFor(email string) []Folder {
	if f, ok := folders[email]; ok {
		return f
	}
	return nil
}

func MessagesFor(email, folder string) []Message {
	if acct, ok := messages[email]; ok {
		if msgs, ok := acct[folder]; ok {
			return msgs
		}
	}
	return nil
}
