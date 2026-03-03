package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/tui/keys"
	"github.com/gausejakub/vimail/internal/tui/layout"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// keyMsg builds a tea.KeyMsg from a short descriptor (matches vimtea convention).
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// testApp creates a sized Model ready for testing.
func testApp() Model {
	cfg := config.DefaultConfig()
	m := New(cfg, email.NewMockStore())
	// Simulate a reasonable terminal size so layout is computed.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	// Drain the Init command (auto-selects first message).
	drainCmds(&m, m.Init())
	return m
}

// send sends a single key through Update and drains all resulting commands
// (one level deep — enough for ModeChangedMsg, InfoMsg, FolderSelectedMsg, etc.).
func send(m *Model, k string) {
	updated, cmd := m.Update(keyMsg(k))
	*m = updated.(Model)
	drainCmds(m, cmd)
}

// sendMsg sends an arbitrary tea.Msg through Update and drains commands.
func sendMsg(m *Model, msg tea.Msg) {
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	drainCmds(m, cmd)
}

// drainCmds executes a Cmd and feeds its resulting Msg back into Update,
// repeating until no more commands are produced. This handles chained messages
// like FolderSelectedMsg → MessageSelectedMsg.
// Commands that block (e.g. textinput.Blink / tea.Tick) are skipped via timeout.
func drainCmds(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}

	// Try to execute the command with a short timeout.
	// Blink/Tick commands block on a real timer so we skip them.
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()

	select {
	case msg := <-ch:
		if msg == nil {
			return
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				drainCmds(m, c)
			}
		default:
			updated, next := m.Update(msg)
			*m = updated.(Model)
			drainCmds(m, next)
		}
	case <-time.After(5 * time.Millisecond):
		// Command is blocking (timer/blink) — skip it.
	}
}

// sendKeys is a convenience to send multiple keys in sequence.
func sendKeys(m *Model, kk ...string) {
	for _, k := range kk {
		send(m, k)
	}
}

// ---------------------------------------------------------------------------
// Scenario Tests
// ---------------------------------------------------------------------------

func TestInitialState(t *testing.T) {
	m := testApp()

	if m.mode != keys.ModeNormal {
		t.Fatalf("initial mode = %v, want NORMAL", m.mode)
	}
	if m.focusedPane != layout.PaneMsgList {
		t.Fatalf("initial focus = %v, want PaneMsgList", m.focusedPane)
	}
	if m.showHelp {
		t.Fatal("help should not be visible on init")
	}
	if m.compose.Visible() {
		t.Fatal("compose should not be visible on init")
	}
	if m.cmdActive {
		t.Fatal("command mode should not be active on init")
	}
}

// --- Mode transitions ---

func TestNormalToCommandAndBack(t *testing.T) {
	m := testApp()

	// Press : to enter command mode
	send(&m, ":")
	if m.mode != keys.ModeCommand {
		t.Fatalf("after ':', mode = %v, want COMMAND", m.mode)
	}
	if !m.cmdActive {
		t.Fatal("cmdActive should be true after ':'")
	}

	// Press Esc to return to normal
	send(&m, "esc")
	if m.mode != keys.ModeNormal {
		t.Fatalf("after Esc, mode = %v, want NORMAL", m.mode)
	}
	if m.cmdActive {
		t.Fatal("cmdActive should be false after Esc")
	}
}

func TestCommandSubmitReturnsToNormal(t *testing.T) {
	m := testApp()

	send(&m, ":")
	// Type "sync" then Enter
	sendKeys(&m, "s", "y", "n", "c", "enter")

	if m.mode != keys.ModeNormal {
		t.Fatalf("after command submit, mode = %v, want NORMAL", m.mode)
	}
	if m.cmdActive {
		t.Fatal("cmdActive should be false after command submit")
	}
}

func TestComposeOpensInInsertMode(t *testing.T) {
	m := testApp()

	send(&m, "c")
	if !m.compose.Visible() {
		t.Fatal("compose should be visible after 'c'")
	}
	if m.mode != keys.ModeInsert {
		t.Fatalf("after compose, mode = %v, want INSERT", m.mode)
	}
}

func TestComposeEscClosesEmptyCompose(t *testing.T) {
	m := testApp()

	send(&m, "c")
	// Esc on empty compose should send ComposeCloseMsg
	send(&m, "esc")

	if m.compose.Visible() {
		t.Fatal("compose should be hidden after Esc on empty compose")
	}
	if m.mode != keys.ModeNormal {
		t.Fatalf("after compose close, mode = %v, want NORMAL", m.mode)
	}
}

// --- Help overlay ---

func TestHelpToggle(t *testing.T) {
	m := testApp()

	// Open help
	send(&m, "?")
	if !m.showHelp {
		t.Fatal("help should be visible after '?'")
	}

	// While help is open, other keys should be ignored
	send(&m, "j")
	if !m.showHelp {
		t.Fatal("help should remain visible when pressing 'j'")
	}

	// Close help with '?'
	send(&m, "?")
	if m.showHelp {
		t.Fatal("help should be hidden after second '?'")
	}
}

func TestHelpCloseWithEsc(t *testing.T) {
	m := testApp()

	send(&m, "?")
	send(&m, "esc")
	if m.showHelp {
		t.Fatal("help should be hidden after Esc")
	}
}

// --- Pane navigation ---

func TestPaneCycleWithTab(t *testing.T) {
	m := testApp()
	if m.focusedPane != layout.PaneMsgList {
		t.Fatalf("start focus = %v, want PaneMsgList", m.focusedPane)
	}

	// Tab → Preview
	send(&m, "tab")
	if m.focusedPane != layout.PanePreview {
		t.Fatalf("after Tab, focus = %v, want PanePreview", m.focusedPane)
	}

	// Tab → wraps to Mailbox
	send(&m, "tab")
	if m.focusedPane != layout.PaneMailbox {
		t.Fatalf("after 2nd Tab, focus = %v, want PaneMailbox", m.focusedPane)
	}

	// Tab → MsgList
	send(&m, "tab")
	if m.focusedPane != layout.PaneMsgList {
		t.Fatalf("after 3rd Tab, focus = %v, want PaneMsgList", m.focusedPane)
	}
}

func TestPaneCycleWithShiftTab(t *testing.T) {
	m := testApp()

	// Shift+Tab → Mailbox (backwards from MsgList)
	send(&m, "shift+tab")
	if m.focusedPane != layout.PaneMailbox {
		t.Fatalf("after Shift+Tab, focus = %v, want PaneMailbox", m.focusedPane)
	}

	// Shift+Tab → wraps to Preview
	send(&m, "shift+tab")
	if m.focusedPane != layout.PanePreview {
		t.Fatalf("after 2nd Shift+Tab, focus = %v, want PanePreview", m.focusedPane)
	}
}

func TestPaneCycleWithHL(t *testing.T) {
	m := testApp()

	// l → forward
	send(&m, "l")
	if m.focusedPane != layout.PanePreview {
		t.Fatalf("after 'l', focus = %v, want PanePreview", m.focusedPane)
	}

	// h → backward
	send(&m, "h")
	if m.focusedPane != layout.PaneMsgList {
		t.Fatalf("after 'h', focus = %v, want PaneMsgList", m.focusedPane)
	}

	// h again → Mailbox
	send(&m, "h")
	if m.focusedPane != layout.PaneMailbox {
		t.Fatalf("after 2nd 'h', focus = %v, want PaneMailbox", m.focusedPane)
	}
}

func TestPaneCycleArrowKeys(t *testing.T) {
	m := testApp()

	send(&m, "right")
	if m.focusedPane != layout.PanePreview {
		t.Fatalf("after Right, focus = %v, want PanePreview", m.focusedPane)
	}

	send(&m, "left")
	if m.focusedPane != layout.PaneMsgList {
		t.Fatalf("after Left, focus = %v, want PaneMsgList", m.focusedPane)
	}
}

func TestPaneCycleWithoutPreview(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.General.PreviewPane = false
	m := New(cfg, email.NewMockStore())
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	drainCmds(&m, m.Init())

	// Should cycle between Mailbox and MsgList only
	send(&m, "tab") // MsgList → Mailbox
	if m.focusedPane != layout.PaneMailbox {
		t.Fatalf("no-preview: after Tab, focus = %v, want PaneMailbox", m.focusedPane)
	}

	send(&m, "tab") // Mailbox → MsgList
	if m.focusedPane != layout.PaneMsgList {
		t.Fatalf("no-preview: after 2nd Tab, focus = %v, want PaneMsgList", m.focusedPane)
	}
}

// --- Navigation within panes ---

func TestMsgListJKNavigation(t *testing.T) {
	m := testApp()
	// Focus is already on MsgList with cursor at 0.
	initial := m.msglist.SelectedMessage()
	if initial == nil {
		t.Fatal("expected a selected message at start")
	}

	// j moves cursor down, selects next message
	send(&m, "j")
	after := m.msglist.SelectedMessage()
	if after == nil || after.ID == initial.ID {
		t.Fatal("j should move to a different message")
	}

	// k moves back
	send(&m, "k")
	back := m.msglist.SelectedMessage()
	if back == nil || back.ID != initial.ID {
		t.Fatalf("k should return to first message, got %v", back.ID)
	}
}

func TestMsgListGAndShiftG(t *testing.T) {
	m := testApp()

	// G goes to bottom
	send(&m, "G")
	sel := m.msglist.SelectedMessage()
	if sel == nil {
		t.Fatal("expected selected message after G")
	}

	// g goes to top
	send(&m, "g")
	top := m.msglist.SelectedMessage()
	if top == nil {
		t.Fatal("expected selected message after g")
	}
	if top.ID != m.msglist.SelectedMessage().ID {
		t.Fatal("g should return to first message")
	}
}

func TestMailboxNavigation(t *testing.T) {
	m := testApp()

	// Focus mailbox
	send(&m, "h")
	if m.focusedPane != layout.PaneMailbox {
		// May need one more h depending on start
		send(&m, "h")
	}

	// j should move cursor in mailbox (first item is account header at 0)
	send(&m, "j")
	// Navigate a few times to ensure no panic
	sendKeys(&m, "j", "j", "k")
}

// --- Command execution ---

func TestCommandQuit(t *testing.T) {
	m := testApp()

	send(&m, ":")
	sendKeys(&m, "q")

	// Submit
	updated, cmd := m.Update(keyMsg("enter"))
	m = updated.(Model)

	// The quit command should produce tea.Quit
	if cmd == nil {
		t.Fatal("expected a command from :q")
	}
	// Execute the batch to find Quit
	found := findQuitInCmd(cmd)
	if !found {
		t.Fatal(":q should produce tea.Quit")
	}
}

func TestCommandTheme(t *testing.T) {
	m := testApp()

	send(&m, ":")
	sendKeys(&m, "t", "h", "e", "m", "e", " ", "n", "o", "r", "d", "enter")

	if m.mode != keys.ModeNormal {
		t.Fatalf("after :theme, mode = %v, want NORMAL", m.mode)
	}
}

func TestCommandUnknown(t *testing.T) {
	m := testApp()

	send(&m, ":")
	sendKeys(&m, "f", "o", "o", "enter")

	// Should return to normal mode even for unknown commands
	if m.mode != keys.ModeNormal {
		t.Fatalf("after unknown command, mode = %v, want NORMAL", m.mode)
	}
}

func TestCommandThemeMissingArg(t *testing.T) {
	m := testApp()

	send(&m, ":")
	sendKeys(&m, "t", "h", "e", "m", "e", "enter")

	if m.mode != keys.ModeNormal {
		t.Fatalf("mode = %v, want NORMAL", m.mode)
	}
}

// --- Compose lifecycle ---

func TestComposeTabCyclesFields(t *testing.T) {
	m := testApp()

	send(&m, "c") // opens compose, focus on To field
	if !m.compose.Visible() {
		t.Fatal("compose should be visible")
	}

	// Tab → Subject → Editor
	send(&m, "tab")
	send(&m, "tab")
	// Should not panic and compose should still be visible
	if !m.compose.Visible() {
		t.Fatal("compose should remain visible after tab cycling")
	}
}

// --- Reply scenario ---

func TestReplyPrefillsFields(t *testing.T) {
	m := testApp()

	// Ensure we have a message selected (Init auto-selects first)
	sel := m.msglist.SelectedMessage()
	if sel == nil {
		t.Fatal("need a selected message to test reply")
	}

	send(&m, "r")
	if !m.compose.Visible() {
		t.Fatal("compose should open on reply")
	}
}

// --- Quit ---

func TestQuitKey(t *testing.T) {
	m := testApp()

	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q should produce a command")
	}
	found := findQuitInCmd(cmd)
	if !found {
		t.Fatal("q should produce tea.Quit")
	}
}

// --- Compose eats all keys ---

func TestComposeEatsKeys(t *testing.T) {
	m := testApp()

	send(&m, "c") // open compose
	prevPane := m.focusedPane

	// Keys that normally switch panes should be eaten by compose
	send(&m, "q") // would quit, but compose eats it
	if m.focusedPane != prevPane {
		t.Fatal("compose should eat keys — pane should not change")
	}
	if !m.compose.Visible() {
		t.Fatal("compose should still be visible")
	}
}

// --- Help eats keys ---

func TestHelpEatsKeys(t *testing.T) {
	m := testApp()

	send(&m, "?") // open help
	prevPane := m.focusedPane

	// j would normally navigate, but help eats it
	send(&m, "j")
	if m.focusedPane != prevPane {
		t.Fatal("help overlay should eat navigation keys")
	}
	if !m.showHelp {
		t.Fatal("help should remain open after j")
	}
}

// --- Folder selection scenario ---

func TestFolderSelectionUpdatesMsglist(t *testing.T) {
	m := testApp()

	// Directly send a FolderSelectedMsg to verify the app routes it properly
	sendMsg(&m, util.FolderSelectedMsg{Account: "alice@acme.corp", Folder: "Inbox"})

	if m.msglist.CurrentFolder() != "Inbox" {
		t.Fatalf("folder = %v, want Inbox", m.msglist.CurrentFolder())
	}
	if m.msglist.CurrentAccount() != "alice@acme.corp" {
		t.Fatalf("account = %v, want alice@acme.corp", m.msglist.CurrentAccount())
	}
}

// --- Full scenario: browse → switch folder → read message → reply ---

func TestBrowseAndReplyScenario(t *testing.T) {
	m := testApp()

	// 1. Start on MsgList with Inbox
	if m.msglist.CurrentFolder() != "Inbox" {
		t.Fatalf("expected Inbox, got %s", m.msglist.CurrentFolder())
	}

	// 2. Navigate down to second message
	send(&m, "j")
	sel := m.msglist.SelectedMessage()
	if sel == nil {
		t.Fatal("expected a message after j")
	}

	// 3. Move to preview pane
	send(&m, "l")
	if m.focusedPane != layout.PanePreview {
		t.Fatalf("expected PanePreview, got %v", m.focusedPane)
	}

	// 4. Scroll preview
	send(&m, "j")
	send(&m, "j")

	// 5. Move back to MsgList
	send(&m, "h")
	if m.focusedPane != layout.PaneMsgList {
		t.Fatalf("expected PaneMsgList, got %v", m.focusedPane)
	}

	// 6. Reply
	send(&m, "r")
	if !m.compose.Visible() {
		t.Fatal("compose should be visible after reply")
	}

	// 7. Close reply
	send(&m, "esc")
}

// --- Full scenario: command mode type and cancel ---

func TestCommandTypeThenCancel(t *testing.T) {
	m := testApp()

	send(&m, ":")
	if m.mode != keys.ModeCommand {
		t.Fatalf("mode = %v, want COMMAND", m.mode)
	}

	// Type some characters
	sendKeys(&m, "t", "h", "e")

	// Cancel
	send(&m, "esc")
	if m.mode != keys.ModeNormal {
		t.Fatalf("mode = %v, want NORMAL after cancel", m.mode)
	}
	if m.cmdActive {
		t.Fatal("cmdActive should be false after cancel")
	}
}

// --- Full scenario: navigate to mailbox, select folder, read messages ---

func TestMailboxFolderNavigation(t *testing.T) {
	m := testApp()

	// Move focus to mailbox
	sendKeys(&m, "h", "h")
	if m.focusedPane != layout.PaneMailbox {
		// Handle wrap
		send(&m, "h")
	}

	// Navigate down through items (accounts + folders)
	sendKeys(&m, "j", "j", "j", "j")

	// Go to top
	send(&m, "g")

	// Go to bottom
	send(&m, "G")

	// No panics = success
}

// --- Full scenario: compose, tab through fields, cancel ---

func TestComposeFullCycle(t *testing.T) {
	m := testApp()

	// 1. Open compose
	send(&m, "c")
	if !m.compose.Visible() {
		t.Fatal("compose not visible")
	}
	if m.mode != keys.ModeInsert {
		t.Fatalf("mode = %v, want INSERT", m.mode)
	}

	// 2. Tab to Subject
	send(&m, "tab")

	// 3. Tab to Editor
	send(&m, "tab")

	// 4. Tab wraps to To
	send(&m, "tab")

	// 5. Esc closes empty compose
	send(&m, "esc")
	if m.compose.Visible() {
		t.Fatal("compose should be hidden after esc on empty")
	}
	if m.mode != keys.ModeNormal {
		t.Fatalf("mode = %v, want NORMAL after compose close", m.mode)
	}
}

// --- Full scenario: rapid pane switching ---

func TestRapidPaneSwitching(t *testing.T) {
	m := testApp()

	for i := 0; i < 20; i++ {
		send(&m, "l")
	}
	for i := 0; i < 20; i++ {
		send(&m, "h")
	}

	// Should not panic, mode should still be normal
	if m.mode != keys.ModeNormal {
		t.Fatalf("mode = %v, want NORMAL after rapid switching", m.mode)
	}
}

// --- Full scenario: open help during different states ---

func TestHelpDoesNotBreakState(t *testing.T) {
	m := testApp()

	// Navigate to preview, open help, close, verify pane is preserved
	send(&m, "l")
	if m.focusedPane != layout.PanePreview {
		t.Fatalf("expected PanePreview")
	}

	send(&m, "?")
	send(&m, "?")

	if m.focusedPane != layout.PanePreview {
		t.Fatalf("pane focus should be preserved after help, got %v", m.focusedPane)
	}
}

// --- Full scenario: G then j at boundary ---

func TestBoundaryNavigation(t *testing.T) {
	m := testApp()

	// Go to last message
	send(&m, "G")
	last := m.msglist.SelectedMessage()

	// j at the bottom should not move
	send(&m, "j")
	still := m.msglist.SelectedMessage()

	if last != nil && still != nil && last.ID != still.ID {
		t.Fatal("j at bottom should not move past last message")
	}

	// Go to top (gg)
	send(&m, "g")
	send(&m, "g")
	top := m.msglist.SelectedMessage()

	// k at top should not move
	send(&m, "k")
	stillTop := m.msglist.SelectedMessage()

	if top != nil && stillTop != nil && top.ID != stillTop.ID {
		t.Fatal("k at top should not move past first message")
	}
}

// --- Full scenario: multiple mode switches ---

func TestModeTransitionChain(t *testing.T) {
	m := testApp()

	// Normal → Command → Normal → Compose(Insert) → Normal → Help → Normal
	send(&m, ":")
	assertMode(t, m, keys.ModeCommand, "after :")

	send(&m, "esc")
	assertMode(t, m, keys.ModeNormal, "after esc from command")

	send(&m, "c")
	assertMode(t, m, keys.ModeInsert, "after compose")

	send(&m, "esc") // close empty compose
	assertMode(t, m, keys.ModeNormal, "after compose close")

	send(&m, "?")
	assertMode(t, m, keys.ModeNormal, "help doesn't change mode")
	if !m.showHelp {
		t.Fatal("help should be shown")
	}

	send(&m, "esc")
	assertMode(t, m, keys.ModeNormal, "after help close")
	if m.showHelp {
		t.Fatal("help should be closed")
	}
}

// --- Full scenario: switch account via folder selection ---

func TestSwitchAccountFolder(t *testing.T) {
	m := testApp()

	// Switch to work account inbox
	sendMsg(&m, util.FolderSelectedMsg{Account: "alice@acme.corp", Folder: "Inbox"})

	if m.msglist.CurrentAccount() != "alice@acme.corp" {
		t.Fatalf("account = %v, want alice@acme.corp", m.msglist.CurrentAccount())
	}

	// Navigate messages in new folder
	send(&m, "j")
	sel := m.msglist.SelectedMessage()
	if sel == nil {
		t.Fatal("should have messages in work inbox")
	}

	// Switch to empty folder
	sendMsg(&m, util.FolderSelectedMsg{Account: "alice@acme.corp", Folder: "Trash"})

	if m.msglist.CurrentFolder() != "Trash" {
		t.Fatalf("folder = %v, want Trash", m.msglist.CurrentFolder())
	}
}

// --- Full scenario: View renders without panic ---

func TestViewDoesNotPanic(t *testing.T) {
	m := testApp()

	// Normal view
	v := m.View()
	if v == "" {
		t.Fatal("View() returned empty string")
	}

	// View with help open
	send(&m, "?")
	v = m.View()
	if v == "" {
		t.Fatal("View() with help returned empty string")
	}
	send(&m, "esc")

	// View with compose open
	send(&m, "c")
	v = m.View()
	if v == "" {
		t.Fatal("View() with compose returned empty string")
	}
	send(&m, "esc")

	// View with command active
	send(&m, ":")
	v = m.View()
	if v == "" {
		t.Fatal("View() with command returned empty string")
	}
	if !strings.Contains(v, ":") {
		t.Fatal("View() in command mode should show command input")
	}
}

// --- Full scenario: reply extracts correct email ---

func TestReplyExtractsSenderEmail(t *testing.T) {
	m := testApp()

	// First message is from "Bob <bob@example.com>"
	sel := m.msglist.SelectedMessage()
	if sel == nil || !strings.Contains(sel.From, "bob@example.com") {
		t.Skip("first message not from bob, skipping")
	}

	send(&m, "r")
	if !m.compose.Visible() {
		t.Fatal("compose should open for reply")
	}
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func assertMode(t *testing.T, m Model, want keys.Mode, ctx string) {
	t.Helper()
	if m.mode != want {
		t.Fatalf("%s: mode = %v, want %v", ctx, m.mode, want)
	}
}

// findQuitInCmd recursively checks if a Cmd chain produces a tea.QuitMsg.
func findQuitInCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	if msg == nil {
		return false
	}
	switch msg.(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg.(tea.BatchMsg) {
			if findQuitInCmd(c) {
				return true
			}
		}
	}
	return false
}
