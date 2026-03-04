package mailbox

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/theme"
	"github.com/gausejakub/vimail/internal/tui/util"
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
	store    email.Store
	accounts []email.Account
	folders  map[string][]email.Folder // keyed by email
	items    []item
	cursor   int
}

func New(store email.Store) Model {
	accts := store.Accounts()
	fmap := make(map[string][]email.Folder)
	for _, a := range accts {
		fmap[a.Email] = store.FoldersFor(a.Email)
	}
	m := Model{
		store:    store,
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
	case util.FolderRefreshMsg:
		m.folders[msg.Account] = m.store.FoldersFor(msg.Account)
		m.items = m.buildItems()
	}
	return m, nil
}

// Reload reloads all accounts and folders from the store.
func (m Model) Reload() Model {
	m.accounts = m.store.Accounts()
	for _, a := range m.accounts {
		m.folders[a.Email] = m.store.FoldersFor(a.Email)
	}
	m.items = m.buildItems()
	return m
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
			prefix := "> "
			text := padRight(prefix+acct.Name, m.width)

			style := lipgloss.NewStyle().Foreground(t.Primary()).Bold(true)
			if isCursor {
				style = lipgloss.NewStyle().Background(t.Selection()).Foreground(t.SelectionText()).Bold(true)
			}
			lines = append(lines, style.Render(text))
		} else {
			acct := m.accounts[it.accountIdx]
			folder := m.folders[acct.Email][it.folderIdx]

			fPrefix := "    "
			if isCursor {
				fPrefix = "  > "
			}

			// Build plain text first, pad to width, then colorize.
			plainLabel := folder.Name
			if folder.UnreadCount > 0 {
				plainLabel = fmt.Sprintf("%s %d", folder.Name, folder.UnreadCount)
			}
			plainLine := fPrefix + plainLabel
			padLen := m.width - len([]rune(plainLine))
			if padLen < 0 {
				padLen = 0
			}

			// Colorize segments.
			var line string
			if folder.UnreadCount > 0 {
				countStr := fmt.Sprintf("%d", folder.UnreadCount)
				nameStr := fPrefix + folder.Name + " "
				fg := t.Text()
				countFg := t.Accent()
				if isCursor {
					fg = t.SelectionText()
					countFg = t.SelectionText()
				}
				nameStyle := lipgloss.NewStyle().Foreground(fg)
				countStyle := lipgloss.NewStyle().Foreground(countFg)
				if isCursor {
					nameStyle = nameStyle.Background(t.Selection()).Bold(true)
					countStyle = countStyle.Background(t.Selection()).Bold(true)
				}
				line = nameStyle.Render(nameStr) + countStyle.Render(countStr)
			} else {
				fg := t.TextMuted()
				style := lipgloss.NewStyle().Foreground(fg)
				if isCursor {
					style = lipgloss.NewStyle().Background(t.Selection()).Foreground(t.SelectionText()).Bold(true)
				}
				line = style.Render(fPrefix + folder.Name)
			}

			// Append padding (plain spaces, with bg if cursor).
			if padLen > 0 {
				pad := fmt.Sprintf("%*s", padLen, "")
				if isCursor {
					line += lipgloss.NewStyle().Background(t.Selection()).Render(pad)
				} else {
					line += pad
				}
			}
			lines = append(lines, line)
		}
	}

	// Pad remaining height
	emptyLine := fmt.Sprintf("%*s", m.width, "")
	for len(lines) < m.height {
		lines = append(lines, emptyLine)
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

func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	pad := width - len(r)
	return s + fmt.Sprintf("%*s", pad, "")
}
