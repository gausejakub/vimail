package tui

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gausejakub/vimail/internal/tui/util"
)

// writeClipboard sends text to the system clipboard: through the platform
// tool when available, otherwise with an OSC 52 escape so it also works over
// SSH. It is a variable so tests can replace it — a test that yanks in the
// reader must never overwrite the developer's real clipboard.
var writeClipboard = func(text string) error {
	err := clipboard.WriteAll(text)
	if err == nil {
		return nil
	}
	seq := osc52.New(text)
	if os.Getenv("TMUX") != "" {
		seq = seq.Tmux()
	}
	if _, werr := fmt.Fprint(os.Stdout, seq); werr != nil {
		return err
	}
	return nil
}

// copyToClipboard puts yanked reader text on the system clipboard: through
// the platform tool (wl-copy, xclip/xsel, pbcopy) when available, otherwise
// with an OSC 52 escape so it also works over SSH.
func copyToClipboard(text string) tea.Cmd {
	// Linewise yanks carry a leading newline in the unnamed register.
	text = strings.TrimPrefix(text, "\n")
	return func() tea.Msg {
		if err := writeClipboard(text); err != nil {
			return util.InfoMsg{Text: "Copy failed: " + err.Error(), IsError: true}
		}
		n := utf8.RuneCountInString(text)
		unit := "characters"
		if n == 1 {
			unit = "character"
		}
		return util.InfoMsg{Text: fmt.Sprintf("Copied %d %s", n, unit)}
	}
}
