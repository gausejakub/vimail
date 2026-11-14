package compose

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/ai"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/keys"
	"github.com/gausejakub/vimail/internal/tui/util"
	"github.com/gausejakub/vimail/pkg/vimtea"
)

const (
	fieldFrom = iota
	fieldTo
	fieldCc
	fieldBcc
	fieldSubject
	fieldCount
	fieldEditor = fieldCount // sentinel: body editor is focused
)

// maxAttachmentBytes caps the total size of a draft's attachments (~25 MB
// once base64-encoded, the limit of most SMTP servers).
const maxAttachmentBytes = 18 * 1024 * 1024

type Model struct {
	width   int
	height  int
	inputs  []textinput.Model
	editor  vimtea.Editor
	focused int
	visible bool

	// Track editor mode to detect changes.
	editorMode vimtea.EditorMode

	// draft holds what the header fields and editor do not show: id,
	// threading headers, reply/forward source and attachments. It is
	// carried unchanged from open to save/send, so a draft prepared over MCP
	// keeps In-Reply-To, References and its source through editing.
	draft email.Draft
	// identities are the From addresses the current account may use; the
	// first is the default.
	identities []string

	// contacts are correspondents from cached mail (best first); suggest is
	// what matches the address being typed in To, Cc or Bcc.
	contacts   []email.Contact
	suggest    []email.Contact
	suggestIdx int

	// AI state
	aiCfg     config.AIConfig
	aiPending bool
}

// attachMsg and detachMsg come from the editor's :attach / :detach commands.
type attachMsg struct{ path string }
type detachMsg struct{ index string }

func New(aiCfg config.AIConfig) Model {
	inputs := make([]textinput.Model, fieldCount)
	field := func(prompt, placeholder string) textinput.Model {
		in := textinput.New()
		in.Prompt = prompt
		in.Placeholder = placeholder
		return in
	}
	inputs[fieldFrom] = field("From:    ", "account address")
	inputs[fieldTo] = field("To:      ", "recipient@example.com, Name <other@example.com>")
	inputs[fieldCc] = field("Cc:      ", "")
	inputs[fieldBcc] = field("Bcc:     ", "")
	inputs[fieldSubject] = field("Subject: ", "Subject")
	inputs[fieldTo].Focus()

	m := Model{
		inputs:  inputs,
		editor:  newEditor(""),
		focused: fieldTo,
		aiCfg:   aiCfg,
	}
	m.registerCommands()
	return m
}

func newEditor(content string) vimtea.Editor {
	opts := []vimtea.EditorOption{
		vimtea.WithEnableStatusBar(false),
		vimtea.WithRelativeNumbers(false),
		vimtea.WithWrap(true),
	}
	if content != "" {
		opts = append(opts, vimtea.WithContent(content))
	}
	return vimtea.NewEditor(opts...)
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.visible {
		return m, nil
	}

	switch msg := msg.(type) {
	case util.AIRequestMsg:
		logging.Info("ai", "AI assist requested", logging.KV("agent", msg.Agent))
		m.aiPending = true
		aiCfg := m.aiCfg
		agent := msg.Agent
		to := m.inputs[fieldTo].Value()
		subject := m.inputs[fieldSubject].Value()
		body := msg.Body
		return m, tea.Batch(
			func() tea.Msg {
				return util.ProcessStartMsg{ID: "ai", Label: "◐ AI thinking…"}
			},
			func() tea.Msg {
				result, err := ai.Generate(aiCfg, agent, to, subject, body)
				return util.AIResponseMsg{Text: result, Err: err}
			},
		)

	case util.AIResponseMsg:
		logging.Info("ai", "AI assist complete", logging.Err(msg.Err))
		m.aiPending = false
		if msg.Err != nil {
			return m, tea.Batch(
				func() tea.Msg { return util.ProcessEndMsg{ID: "ai"} },
				func() tea.Msg { return util.InfoMsg{Text: msg.Err.Error(), IsError: true} },
			)
		}
		// Preserve quoted lines from the original body and only replace the new text
		quoted := extractQuotedTail(m.editor.GetBuffer().Text())
		newBody := strings.TrimRight(msg.Text, "\n")
		if quoted != "" {
			newBody = newBody + "\n" + quoted
		}
		m.editor = newEditor(newBody)
		m.registerCommands()
		return m, tea.Batch(
			func() tea.Msg { return util.ProcessEndMsg{ID: "ai"} },
			func() tea.Msg { return util.InfoMsg{Text: "AI draft ready"} },
		)

	case attachMsg:
		return m.attach(msg.path)

	case detachMsg:
		return m.detach(msg.index)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+s":
			d, err := m.currentDraft(true)
			if err != nil {
				return m, func() tea.Msg { return util.InfoMsg{Text: err.Error(), IsError: true} }
			}
			return m, func() tea.Msg { return util.ComposeSubmitMsg{Draft: d} }

		case "tab":
			return m.cycleField(1)

		case "shift+tab":
			return m.cycleField(-1)
		}

		// Field-specific handling
		if m.focused == fieldEditor {
			return m.updateEditor(msg)
		}

		// Recipient suggestions: Enter takes the highlighted one, arrows choose.
		if len(m.suggest) > 0 {
			switch msg.String() {
			case "enter":
				return m.acceptSuggestion(), nil
			case "down", "ctrl+n":
				m.suggestIdx = (m.suggestIdx + 1) % len(m.suggest)
				return m, nil
			case "up", "ctrl+p":
				m.suggestIdx = (m.suggestIdx + len(m.suggest) - 1) % len(m.suggest)
				return m, nil
			}
		}

		// To/Subject: Esc saves draft and closes compose
		if msg.String() == "esc" {
			return m, m.saveDraftCmd()
		}

		// Delegate to the focused textinput
		var cmd tea.Cmd
		m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
		return m.refreshSuggestions(), cmd
	}

	// Non-key messages: forward to active input or editor
	if m.focused == fieldEditor {
		updated, cmd := m.editor.Update(msg)
		m.editor = updated.(vimtea.Editor)
		return m, cmd
	}

	var cmds []tea.Cmd
	for i := range m.inputs {
		var cmd tea.Cmd
		m.inputs[i], cmd = m.inputs[i].Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m Model) updateEditor(msg tea.KeyMsg) (Model, tea.Cmd) {
	// In normal mode, Esc saves draft and closes compose
	if msg.String() == "esc" && m.editor.GetMode() == vimtea.ModeNormal {
		return m, m.saveDraftCmd()
	}

	updated, cmd := m.editor.Update(msg)
	m.editor = updated.(vimtea.Editor)

	// Check if vimtea mode changed and emit ModeChangedMsg
	var cmds []tea.Cmd
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	newMode := m.editor.GetMode()
	if newMode != m.editorMode {
		m.editorMode = newMode
		appMode := vimteaModeToKeys(newMode)
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: appMode}
		})
	}

	return m, tea.Batch(cmds...)
}

func (m Model) cycleField(dir int) (Model, tea.Cmd) {
	m.suggest, m.suggestIdx = nil, 0
	total := fieldCount + 1 // To, Subject, Editor

	// Blur current
	if m.focused < fieldCount {
		m.inputs[m.focused].Blur()
	}

	m.focused = (m.focused + dir + total) % total

	var cmds []tea.Cmd

	// Focus new
	if m.focused < fieldCount {
		m.inputs[m.focused].Focus()
		cmds = append(cmds, textinput.Blink)
		// Textinputs imply INSERT mode
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: keys.ModeInsert}
		})
	} else {
		// Entering editor — set to normal mode initially
		m.editorMode = m.editor.GetMode()
		appMode := vimteaModeToKeys(m.editorMode)
		cmds = append(cmds, func() tea.Msg {
			return keys.ModeChangedMsg{Mode: appMode}
		})
	}

	m = m.updateFieldStyles()
	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if !m.visible {
		return ""
	}

	t := theme.Current()
	dialogWidth, dialogHeight := m.dialogSize()
	// border (2) + padding (4). Never below a few columns: in a very small
	// terminal the arithmetic below would go negative and panic, taking the
	// message being written with it.
	innerWidth := max(dialogWidth-6, 4)

	// Set input widths
	for i := range m.inputs {
		m.inputs[i].Width = max(innerWidth-len(m.inputs[i].Prompt), 1)
	}

	title := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render("  " + m.title())

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for _, input := range m.inputs {
		lines = append(lines, "  "+input.View())
	}
	if line := m.attachmentLine(); line != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(t.Accent()).Render(truncate("  Attach:  "+line, innerWidth+2)))
	}
	for i, c := range m.suggest {
		style, mark := lipgloss.NewStyle().Foreground(t.TextMuted()), "  "
		if i == m.suggestIdx {
			style, mark = lipgloss.NewStyle().Foreground(t.Primary()).Bold(true), "→ "
		}
		lines = append(lines, style.Render(truncate("         "+mark+email.SanitizeLine(c.Formatted()), innerWidth+2)))
	}
	lines = append(lines, lipgloss.NewStyle().
		Foreground(t.BorderNormal()).
		Render("  "+strings.Repeat("─", innerWidth-2)))

	// Header takes: title, blank, To, Subject, separator = 5 lines
	// Hint line = 1 line, padding top/bottom = 2 lines, border = 2 lines
	headerLines := len(lines)
	chrome := headerLines + 1 + 4 // +1 hint, +4 border+padding
	bodyHeight := dialogHeight - chrome
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	// Size the editor and render
	sized, _ := m.editor.SetSize(innerWidth, bodyHeight)
	m.editor = sized.(vimtea.Editor)
	editorView := m.editor.View()

	// Dim the editor when not focused
	if m.focused != fieldEditor {
		editorView = lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Render(editorView)
	}

	lines = append(lines, editorView)
	var hint string
	switch {
	case len(m.suggest) > 0:
		hint = "  Enter: use suggestion | ↑↓: choose | Tab: next field"
	case m.aiPending:
		hint = "  Thinking..."
	case m.focused == fieldEditor && m.editor.GetMode() == vimtea.ModeCommand:
		hint = "  " + m.editor.GetStatusText()
	default:
		hint = "  Tab: next field | :attach <path> :detach <n> | :ai: assist | Ctrl+S: send | Esc: save draft"
	}
	lines = append(lines, lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Render(hint))

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BorderFocused()).
		Padding(1, 2).
		Width(dialogWidth).
		Height(dialogHeight).
		Render(content)
}

// Show opens an empty composer.
func (m Model) Show() Model {
	return m.open(email.Draft{}, fieldTo)
}

// WithIdentities sets the From addresses the current account may use; the
// first is the default for a message that names none.
func (m Model) WithIdentities(ids []string) Model {
	m.identities = ids
	if m.inputs[fieldFrom].Value() == "" && len(ids) > 0 {
		m.inputs[fieldFrom].SetValue(email.SanitizeLine(ids[0]))
	}
	return m
}

// ShowDraft opens a saved draft with every field, including the hidden
// threading headers, source and attachments.
func (m Model) ShowDraft(d email.Draft) Model {
	return m.open(d, fieldEditor)
}

// ShowReply opens a new reply or forward prepared with email.ReplyRecipients
// and friends (d has no ID yet).
func (m Model) ShowReply(d email.Draft) Model {
	d.ID = ""
	return m.open(d, fieldEditor)
}

func (m Model) open(d email.Draft, focus int) Model {
	m.visible = true
	m.focused = focus
	m.draft = d
	m.identities = nil
	m.suggest, m.suggestIdx = nil, 0
	for i := range m.inputs {
		m.inputs[i].SetValue("")
		m.inputs[i].Blur()
	}
	// A draft is not necessarily the user's own typing: an AI client can save
	// one through the MCP server, and the editor draws its buffer as is.
	m.inputs[fieldFrom].SetValue(email.SanitizeLine(d.From))
	m.inputs[fieldTo].SetValue(email.SanitizeLine(email.JoinAddresses(d.To)))
	m.inputs[fieldCc].SetValue(email.SanitizeLine(email.JoinAddresses(d.Cc)))
	m.inputs[fieldBcc].SetValue(email.SanitizeLine(email.JoinAddresses(d.Bcc)))
	m.inputs[fieldSubject].SetValue(email.SanitizeLine(d.Subject))
	if focus < fieldCount {
		m.inputs[focus].Focus()
	}
	m.editor = newEditor(email.SanitizeText(d.Body))
	m.editorMode = vimtea.ModeNormal
	m.aiPending = false
	m.registerCommands()
	m = m.updateFieldStyles()
	return m
}

func (m Model) Hide() Model {
	m.visible = false
	return m
}

func (m Model) Visible() bool {
	return m.visible
}

func (m Model) SetSize(w, h int) Model {
	m.width = w
	m.height = h
	return m
}

func (m Model) EditorMode() vimtea.EditorMode {
	return m.editor.GetMode()
}

func (m Model) dialogSize() (int, int) {
	w := m.width * 80 / 100
	if w < 40 {
		w = 40
	}
	if w > m.width-2 {
		w = m.width - 2
	}

	h := m.height * 70 / 100
	if h < 14 {
		h = 14
	}
	if h > m.height-2 {
		h = m.height - 2
	}

	return w, h
}

func (m Model) updateFieldStyles() Model {
	t := theme.Current()
	for i := range m.inputs {
		if i == m.focused {
			m.inputs[i].PromptStyle = lipgloss.NewStyle().Foreground(t.Primary()).Bold(true)
			m.inputs[i].TextStyle = lipgloss.NewStyle().Foreground(t.Text())
		} else {
			m.inputs[i].PromptStyle = lipgloss.NewStyle().Foreground(t.TextMuted())
			m.inputs[i].TextStyle = lipgloss.NewStyle().Foreground(t.TextMuted())
		}
	}
	return m
}

func (m Model) saveDraftCmd() tea.Cmd {
	d, _ := m.currentDraft(false)
	// If everything is empty, just close without saving
	if len(d.To) == 0 && len(d.Cc) == 0 && len(d.Bcc) == 0 && d.Subject == "" && d.Body == "" && len(d.Attachments) == 0 {
		return func() tea.Msg { return util.ComposeCloseMsg{} }
	}
	return func() tea.Msg { return util.ComposeSaveDraftMsg{Draft: d} }
}

// DraftID returns the id of the draft being edited ("" for a new message).
func (m Model) DraftID() string {
	return m.draft.ID
}

// currentDraft assembles the draft from the header fields, the editor and
// the hidden state. With strict, recipients must parse and From must be one
// of the account's identities (the send path); without, unparseable input
// is kept as typed so saving a half-written draft never loses it.
func (m Model) currentDraft(strict bool) (email.Draft, error) {
	d := m.draft
	d.Subject = m.inputs[fieldSubject].Value()
	d.Body = m.editor.GetBuffer().Text()
	var err error
	field := func(i int, name string) []string {
		v := m.inputs[i].Value()
		list, perr := email.SplitAddressField(v)
		if perr != nil {
			if err == nil {
				err = fmt.Errorf("%s: %v", name, perr)
			}
			return []string{strings.TrimSpace(v)}
		}
		return list
	}
	d.To = field(fieldTo, "To")
	d.Cc = field(fieldCc, "Cc")
	d.Bcc = field(fieldBcc, "Bcc")
	d.From = strings.TrimSpace(m.inputs[fieldFrom].Value())
	if strict {
		if err != nil {
			return d, err
		}
		if len(d.To)+len(d.Cc)+len(d.Bcc) == 0 {
			return d, fmt.Errorf("add at least one recipient")
		}
		if d.From != "" && len(m.identities) > 0 {
			// Strict: From must be exactly one mailbox (see
			// email.SingleAddress), not a string one mailbox of which matches.
			ok := false
			if from, single := email.SingleAddress(d.From); single {
				for _, id := range m.identities {
					if have, valid := email.SingleAddress(id); valid && have == from {
						ok = true
						break
					}
				}
			}
			if !ok {
				return d, fmt.Errorf("From %s is not an identity of this account (add it under identities in the config)", email.AddrSpec(d.From))
			}
		}
	}
	return d, nil
}

func (m *Model) registerCommands() {
	m.editor.AddCommand("ai", func(buf vimtea.Buffer, args []string) tea.Cmd {
		body := buf.Text()
		var agent string
		if len(args) > 0 {
			agent = args[0]
		}
		return func() tea.Msg {
			return util.AIRequestMsg{Agent: agent, Body: body}
		}
	})
	m.editor.AddCommand("attach", func(buf vimtea.Buffer, args []string) tea.Cmd {
		path := strings.Join(args, " ")
		return func() tea.Msg { return attachMsg{path: path} }
	})
	m.editor.AddCommand("detach", func(buf vimtea.Buffer, args []string) tea.Cmd {
		index := strings.Join(args, " ")
		return func() tea.Msg { return detachMsg{index: index} }
	})
}

func info(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return util.InfoMsg{Text: text, IsError: isErr} }
}

// attach adds a local file typed by the user (":attach ~/report.pdf").
func (m Model) attach(path string) (Model, tea.Cmd) {
	path = strings.TrimSpace(path)
	if path == "" {
		return m, info("usage: :attach <path>", true)
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return m, info(err.Error(), true)
	}
	if !st.Mode().IsRegular() {
		return m, info(path+" is not a regular file", true)
	}
	total := st.Size()
	for _, a := range m.draft.Attachments {
		total += int64(a.AttachmentSize())
	}
	if total > maxAttachmentBytes {
		return m, info(fmt.Sprintf("attachments would total %s; the limit is %s", humanSize(int(total)), humanSize(maxAttachmentBytes)), true)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m, info(err.Error(), true)
	}
	name := filepath.Base(path)
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		ct = mt
	}
	m.draft.Attachments = append(append([]email.DraftAttachment(nil), m.draft.Attachments...),
		email.DraftAttachment{Filename: name, ContentType: ct, Data: data, Size: len(data)})
	return m, info("attached "+name, false)
}

// detach removes attachment n (1-based, as listed), or the last one.
func (m Model) detach(index string) (Model, tea.Cmd) {
	n := len(m.draft.Attachments)
	if n == 0 {
		return m, info("no attachments", true)
	}
	i := n
	if index = strings.TrimSpace(index); index != "" {
		v, err := strconv.Atoi(index)
		if err != nil || v < 1 || v > n {
			return m, info(fmt.Sprintf("usage: :detach <1-%d>", n), true)
		}
		i = v
	}
	name := m.draft.Attachments[i-1].Filename
	atts := append([]email.DraftAttachment(nil), m.draft.Attachments[:i-1]...)
	m.draft.Attachments = append(atts, m.draft.Attachments[i:]...)
	return m, info("removed "+email.SanitizeLine(name), false)
}

func (m Model) attachmentLine() string {
	var parts []string
	for i, a := range m.draft.Attachments {
		parts = append(parts, fmt.Sprintf("%d. %s (%s)", i+1, email.SanitizeLine(a.Filename), humanSize(a.AttachmentSize())))
	}
	return strings.Join(parts, "  ")
}

func (m Model) title() string {
	switch {
	case m.draft.Source != nil && m.draft.Source.Kind == email.SourceReply:
		return "Reply"
	case m.draft.Source != nil && m.draft.Source.Kind == email.SourceForward:
		return "Forward"
	case m.draft.ID != "":
		return "Edit Draft"
	}
	return "Compose New Message"
}

func humanSize(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%d KB", n/1024)
	}
	return fmt.Sprintf("%d B", n)
}

func truncate(s string, width int) string {
	if r := []rune(s); width > 1 && len(r) > width {
		return string(r[:width-1]) + "…"
	}
	return s
}

func extractQuotedTail(body string) string {
	lines := strings.Split(body, "\n")

	// Find where the trailing quoted block starts by scanning backwards
	quoteStart := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, ">") || trimmed == "" {
			quoteStart = i
		} else {
			break
		}
	}

	if quoteStart >= len(lines) {
		return ""
	}

	return strings.Join(lines[quoteStart:], "\n")
}

func vimteaModeToKeys(m vimtea.EditorMode) keys.Mode {
	switch m {
	case vimtea.ModeInsert:
		return keys.ModeInsert
	case vimtea.ModeVisual:
		return keys.ModeVisual
	case vimtea.ModeCommand:
		return keys.ModeCommand
	default:
		return keys.ModeNormal
	}
}

// CurrentDraftForTest exposes the assembled draft (lenient) to tests.
func (m Model) CurrentDraftForTest() (email.Draft, error) {
	return m.currentDraft(false)
}

// maxSuggestions is how many matching contacts are offered at once.
const maxSuggestions = 5

// SetContacts gives the composer the correspondents to complete from.
func (m Model) SetContacts(contacts []email.Contact) Model {
	m.contacts = contacts
	return m
}

func (m Model) isRecipientField() bool {
	return m.focused == fieldTo || m.focused == fieldCc || m.focused == fieldBcc
}

// currentToken returns what is being typed after the last comma of the
// focused recipient field, and where it starts.
func (m Model) currentToken() (token string, start int) {
	value := m.inputs[m.focused].Value()
	start = strings.LastIndex(value, ",") + 1
	return strings.TrimSpace(value[start:]), start
}

// refreshSuggestions recomputes the suggestions after a keystroke. Contacts
// already in the field are not offered again.
func (m Model) refreshSuggestions() Model {
	m.suggest, m.suggestIdx = nil, 0
	if !m.isRecipientField() || len(m.contacts) == 0 {
		return m
	}
	token, start := m.currentToken()
	if len([]rune(token)) < 2 {
		return m
	}
	have := email.NewAddressSet(strings.Split(m.inputs[m.focused].Value()[:start], ",")...)
	for _, c := range email.MatchContacts(m.contacts, token, maxSuggestions*3) {
		if have[c.Address] || strings.EqualFold(c.Address, token) {
			continue
		}
		m.suggest = append(m.suggest, c)
		if len(m.suggest) == maxSuggestions {
			break
		}
	}
	return m
}

// acceptSuggestion replaces the address being typed with the highlighted
// contact and leaves the field ready for the next recipient.
func (m Model) acceptSuggestion() Model {
	if len(m.suggest) == 0 || !m.isRecipientField() {
		return m
	}
	_, start := m.currentToken()
	value := m.inputs[m.focused].Value()[:start]
	if value != "" {
		value += " "
	}
	m.inputs[m.focused].SetValue(value + email.SanitizeLine(m.suggest[m.suggestIdx].Formatted()) + ", ")
	m.inputs[m.focused].CursorEnd()
	m.suggest, m.suggestIdx = nil, 0
	return m
}

// SuggestionsForTest exposes the current suggestions to tests.
func (m Model) SuggestionsForTest() []email.Contact { return m.suggest }
