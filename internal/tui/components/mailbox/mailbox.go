package mailbox

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gause/vmail/internal/mock"
	"github.com/gause/vmail/internal/theme"
	"github.com/gause/vmail/internal/tui/util"
)

// item represents a row in the flat mailbox list — either an account header or a folder.
type item struct {
	isAccount  bool
	accountIdx int
	folderIdx  int // -1 for account headers
}

type Model struct {
	width    int
	height   int
	focused  bool
	accounts []mock.Account
	folders  map[string][]mock.Folder // keyed by email
	items    []item
	cursor   int
}

func New() Model {
	accts := mock.Accounts
	fmap := make(map[string][]mock.Folder)
	for _, a := range accts {
		fmap[a.Email] = mock.FoldersFor(a.Email)
	}
	m := Model{
		accounts: accts,
		folders:  fmap,
	}
	m.items = m.buildItems()
	return m
}

// buildItems creates the flat list of account headers + folder rows.
func (m Model) buildItems() []item {
	var items []item
	for ai, acct := range m.accounts {
		items = append(items, item{isAccount: true, accountIdx: ai, folderIdx: -1})
		for fi := range m.folders[acct.Email] {
			items = append(items, item{isAccount: false, accountIdx: ai, folderIdx: fi})
		}
	}
	return items
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

// HandleKey processes a key press and returns the updated model + any command.
func (m Model) HandleKey(key string) (Model, tea.Cmd) {
	switch key {
	case "j", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			return m, m.emitIfFolder()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			return m, m.emitIfFolder()
		}
	case "g":
		m.cursor = 0
		return m, m.emitIfFolder()
	case "G":
		if len(m.items) > 0 {
			m.cursor = len(m.items) - 1
			return m, m.emitIfFolder()
		}
	}
	return m, nil
}

// emitIfFolder returns a FolderSelectedMsg command if the cursor is on a folder row.
func (m Model) emitIfFolder() tea.Cmd {
	if m.cursor >= len(m.items) {
		return nil
	}
	it := m.items[m.cursor]
	if it.isAccount {
		return nil
	}
	acct := m.accounts[it.accountIdx]
	folders := m.folders[acct.Email]
	if it.folderIdx < 0 || it.folderIdx >= len(folders) {
		return nil
	}
	folder := folders[it.folderIdx]
	return func() tea.Msg {
		return util.FolderSelectedMsg{
			Account: acct.Email,
			Folder:  folder.Name,
		}
	}
}

func (m Model) View() string {
	t := theme.Current()
	var lines []string

	for i, it := range m.items {
		isCursor := i == m.cursor && m.focused

		if it.isAccount {
			acct := m.accounts[it.accountIdx]
			style := lipgloss.NewStyle().Width(m.width)
			prefix := "  "

			if isCursor {
				style = style.Background(t.Selection()).Foreground(t.SelectionText()).Bold(true)
				prefix = "▸ "
			} else {
				style = style.Foreground(t.Primary()).Bold(true)
				prefix = "▸ "
			}
			lines = append(lines, style.Render(prefix+acct.Name))
		} else {
			acct := m.accounts[it.accountIdx]
			folder := m.folders[acct.Email][it.folderIdx]

			fStyle := lipgloss.NewStyle().Width(m.width)
			fPrefix := "    "

			if isCursor {
				fStyle = fStyle.Background(t.Selection()).Foreground(t.SelectionText()).Bold(true)
				fPrefix = "  ▸ "
			} else if folder.UnreadCount > 0 {
				fStyle = fStyle.Foreground(t.Text())
			} else {
				fStyle = fStyle.Foreground(t.TextMuted())
			}

			label := folder.Name
			if folder.UnreadCount > 0 {
				countStyle := lipgloss.NewStyle().Foreground(t.Accent())
				if isCursor {
					countStyle = countStyle.Foreground(t.SelectionText())
				}
				label = fmt.Sprintf("%s %s", folder.Name, countStyle.Render(fmt.Sprintf("%d", folder.UnreadCount)))
			}
			lines = append(lines, fStyle.Render(fPrefix+label))
		}
	}

	// Pad remaining height
	for len(lines) < m.height {
		lines = append(lines, lipgloss.NewStyle().Width(m.width).Render(""))
	}

	result := ""
	for i, line := range lines {
		if i >= m.height {
			break
		}
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

func (m Model) Focus() Model {
	m.focused = true
	return m
}

func (m Model) Blur() Model {
	m.focused = false
	return m
}

func (m Model) SetSize(w, h int) Model {
	m.width = w
	m.height = h
	return m
}

// SelectedEmail returns the email of the account the cursor is on (or the parent account of the folder).
func (m Model) SelectedEmail() string {
	if m.cursor < len(m.items) {
		it := m.items[m.cursor]
		if it.accountIdx < len(m.accounts) {
			return m.accounts[it.accountIdx].Email
		}
	}
	return ""
}

// SelectedFolderName returns the folder name if cursor is on a folder, or "Inbox" otherwise.
func (m Model) SelectedFolderName() string {
	if m.cursor < len(m.items) {
		it := m.items[m.cursor]
		if !it.isAccount && it.folderIdx >= 0 {
			acct := m.accounts[it.accountIdx]
			folders := m.folders[acct.Email]
			if it.folderIdx < len(folders) {
				return folders[it.folderIdx].Name
			}
		}
	}
	return "Inbox"
}

// SelectedAccountName returns the account name the cursor is on.
func (m Model) SelectedAccountName() string {
	if m.cursor < len(m.items) {
		it := m.items[m.cursor]
		if it.accountIdx < len(m.accounts) {
			return m.accounts[it.accountIdx].Name
		}
	}
	return ""
}

// SelectFolder moves the cursor to the given account+folder combination.
func (m Model) SelectFolder(email, folder string) Model {
	for i, it := range m.items {
		if it.isAccount {
			continue
		}
		acct := m.accounts[it.accountIdx]
		if acct.Email != email {
			continue
		}
		folders := m.folders[acct.Email]
		if it.folderIdx >= 0 && it.folderIdx < len(folders) && folders[it.folderIdx].Name == folder {
			m.cursor = i
			return m
		}
	}
	return m
}
