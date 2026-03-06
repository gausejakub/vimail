package compose

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/ai"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/keys"
	"github.com/gausejakub/vimail/internal/tui/util"
	"github.com/gausejakub/vimail/pkg/vimtea"
)

const (
	fieldTo = iota
	fieldSubject
	fieldCount
	fieldEditor = fieldCount // sentinel: body editor is focused
)

type Model struct {
	width   int
	height  int
	inputs  []textinput.Model
	editor  vimtea.Editor
	focused int
	visible bool

	// Track editor mode to detect changes.
	editorMode vimtea.EditorMode

	// Draft tracking
	draftID string // non-empty when editing an existing draft

	// AI state
	aiCfg     config.AIConfig
	aiPending bool
}

func New(aiCfg config.AIConfig) Model {
	inputs := make([]textinput.Model, fieldCount)

	to := textinput.New()
	to.Placeholder = "recipient@example.com"
	to.Prompt = "To:      "
	to.Focus()
	inputs[fieldTo] = to

	subj := textinput.New()
	subj.Placeholder = "Subject"
	subj.Prompt = "Subject: "
	inputs[fieldSubject] = subj

	m := Model{
		inputs:  inputs,
		editor:  newEditor(""),
		focused: fieldTo,
		aiCfg:   aiCfg,
	}
	m.registerAICommand()
	return m
}

func newEditor(content string) vimtea.Editor {
	opts := []vimtea.EditorOption{
		vimtea.WithEnableStatusBar(false),
		vimtea.WithRelativeNumbers(false),
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
		m.aiPending = false
		if msg.Err != nil {
			return m, tea.Batch(
				func() tea.Msg { return util.ProcessEndMsg{ID: "ai"} },
				func() tea.Msg { return util.InfoMsg{Text: msg.Err.Error(), IsError: true} },
			)
		}
		m.editor = newEditor(msg.Text)
		m.registerAICommand()
		return m, tea.Batch(
			func() tea.Msg { return util.ProcessEndMsg{ID: "ai"} },
			func() tea.Msg { return util.InfoMsg{Text: "AI draft ready"} },
		)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+s":
			return m, func() tea.Msg {
				return util.ComposeSubmitMsg{
					To:      m.inputs[fieldTo].Value(),
					Subject: m.inputs[fieldSubject].Value(),
					Body:    m.editor.GetBuffer().Text(),
				}
			}

		case "tab":
			return m.cycleField(1)

		case "shift+tab":
			return m.cycleField(-1)
		}

		// Field-specific handling
		if m.focused == fieldEditor {
			return m.updateEditor(msg)
		}

		// To/Subject: Esc saves draft and closes compose
		if msg.String() == "esc" {
			return m, m.saveDraftCmd()
		}

		// Delegate to the focused textinput
		var cmd tea.Cmd
		m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
		return m, cmd
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
	innerWidth := dialogWidth - 6 // border (2) + padding (4)

	// Set input widths
	for i := range m.inputs {
		m.inputs[i].Width = innerWidth - len(m.inputs[i].Prompt)
	}

	title := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render("  Compose New Message")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for _, input := range m.inputs {
		lines = append(lines, "  "+input.View())
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
	case m.aiPending:
		hint = "  Thinking..."
	case m.focused == fieldEditor && m.editor.GetMode() == vimtea.ModeCommand:
		hint = "  " + m.editor.GetStatusText()
	default:
		hint = "  Tab: next field | :ai: assist | Ctrl+S: send | Esc: cancel"
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

func (m Model) Show() Model {
	m.visible = true
	m.focused = fieldTo
	m.draftID = ""
	for i := range m.inputs {
		m.inputs[i].SetValue("")
		m.inputs[i].Blur()
	}
	m.inputs[fieldTo].Focus()
	m.editor = newEditor("")
	m.editorMode = vimtea.ModeNormal
	m.aiPending = false
	m.registerAICommand()
	m = m.updateFieldStyles()
	return m
}

func (m Model) ShowDraft(id, to, subject, body string) Model {
	m.visible = true
	m.focused = fieldEditor
	m.draftID = id
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.inputs[fieldTo].SetValue(to)
	m.inputs[fieldSubject].SetValue(subject)
	m.editor = newEditor(body)
	m.editorMode = vimtea.ModeNormal
	m.aiPending = false
	m.registerAICommand()
	m = m.updateFieldStyles()
	return m
}

func (m Model) ShowReply(to, subject, quotedBody string) Model {
	m.visible = true
	m.focused = fieldEditor
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.inputs[fieldTo].SetValue(to)
	m.inputs[fieldSubject].SetValue(subject)
	m.editor = newEditor(quotedBody)
	m.editorMode = vimtea.ModeNormal
	m.aiPending = false
	m.registerAICommand()
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
	to := m.inputs[fieldTo].Value()
	subject := m.inputs[fieldSubject].Value()
	body := m.editor.GetBuffer().Text()

	// If everything is empty, just close without saving
	if to == "" && subject == "" && body == "" {
		return func() tea.Msg { return util.ComposeCloseMsg{} }
	}

	return func() tea.Msg {
		return util.ComposeSaveDraftMsg{
			DraftID: m.draftID,
			To:      to,
			Subject: subject,
			Body:    body,
		}
	}
}

func (m Model) DraftID() string {
	return m.draftID
}

func (m *Model) registerAICommand() {
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
