package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
)

// omarchyTheme adapts vimail to the Omarchy theming ecosystem
// (https://omarchy.org). Omarchy materializes the active theme's semantic
// palette at ~/.local/state/omarchy/current/theme/colors.toml and also pushes
// it into every terminal's ANSI 0-15 slots via OSC sequences.
//
// Strategy: when colors.toml exists we use the exact semantic palette
// (accent, selection, muted, background/foreground shades). When it doesn't
// — non-omarchy systems, or older omarchy — we fall back per-key to ANSI-16,
// which omarchy terminals repaint on theme switch anyway. The palette is
// re-read every time the theme is (re)activated with `:theme omarchy`, so a
// theme switch in omarchy is picked up without restarting vimail.
type omarchyTheme struct {
	mu     sync.RWMutex
	colors map[string]lipgloss.Color
}

// omarchyColorsFile is a func var so tests can point it at a fixture.
var omarchyColorsFile = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "colors.toml")
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func init() {
	t := &omarchyTheme{}
	t.Refresh()
	Register(t)
}

// Refresh re-reads the omarchy palette; called by SetCurrent on activation.
func (t *omarchyTheme) Refresh() {
	loaded := map[string]lipgloss.Color{}
	if path := omarchyColorsFile(); path != "" {
		var raw map[string]any
		if _, err := toml.DecodeFile(path, &raw); err == nil {
			for k, v := range raw {
				if s, ok := v.(string); ok && hexColorRe.MatchString(s) {
					loaded[k] = lipgloss.Color(s)
				}
			}
		}
	}
	t.mu.Lock()
	t.colors = loaded
	t.mu.Unlock()
}

// color returns the first present semantic key, else the ANSI fallback.
// Omarchy derives missing shades itself, but community themes may omit
// keys, so each role lists alternates in preference order.
func (t *omarchyTheme) color(ansi string, keys ...string) lipgloss.Color {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, k := range keys {
		if c, ok := t.colors[k]; ok {
			return c
		}
	}
	return lipgloss.Color(ansi)
}

func (t *omarchyTheme) Name() string { return "omarchy" }

func (t *omarchyTheme) Primary() lipgloss.Color   { return t.color("4", "accent", "blue") }
func (t *omarchyTheme) Secondary() lipgloss.Color { return t.color("5", "magenta") }
func (t *omarchyTheme) Accent() lipgloss.Color    { return t.color("6", "cyan") }

func (t *omarchyTheme) Text() lipgloss.Color      { return t.color("7", "foreground") }
func (t *omarchyTheme) TextMuted() lipgloss.Color { return t.color("8", "muted") }
func (t *omarchyTheme) TextEmphasized() lipgloss.Color {
	return t.color("15", "bright_foreground", "foreground")
}

func (t *omarchyTheme) Background() lipgloss.Color { return t.color("0", "background") }
func (t *omarchyTheme) BackgroundSecondary() lipgloss.Color {
	return t.color("0", "lighter_background", "background")
}
func (t *omarchyTheme) BackgroundDarker() lipgloss.Color {
	return t.color("0", "darker_background", "dark_background", "background")
}

func (t *omarchyTheme) BorderNormal() lipgloss.Color  { return t.color("8", "muted") }
func (t *omarchyTheme) BorderFocused() lipgloss.Color { return t.color("4", "accent", "blue") }
func (t *omarchyTheme) BorderDim() lipgloss.Color {
	return t.color("0", "dark_background", "selection")
}

func (t *omarchyTheme) Selection() lipgloss.Color { return t.color("4", "selection") }
func (t *omarchyTheme) SelectionText() lipgloss.Color {
	return t.color("15", "bright_foreground", "foreground")
}

func (t *omarchyTheme) Error() lipgloss.Color   { return t.color("1", "red") }
func (t *omarchyTheme) Warning() lipgloss.Color { return t.color("3", "yellow") }
func (t *omarchyTheme) Success() lipgloss.Color { return t.color("2", "green") }
func (t *omarchyTheme) Info() lipgloss.Color    { return t.color("6", "cyan") }

func (t *omarchyTheme) NormalMode() lipgloss.Color  { return t.color("4", "accent", "blue") }
func (t *omarchyTheme) InsertMode() lipgloss.Color  { return t.color("2", "green") }
func (t *omarchyTheme) VisualMode() lipgloss.Color  { return t.color("3", "yellow") }
func (t *omarchyTheme) CommandMode() lipgloss.Color { return t.color("5", "magenta") }
