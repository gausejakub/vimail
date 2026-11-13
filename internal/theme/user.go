package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
)

// User themes are TOML files in ~/.config/vimail/themes: <name>.toml becomes
// the theme <name>. Every key is optional; a missing one is taken from the
// base theme (the built-in "vimail" unless the file says otherwise).
//
//	base = "gruvbox"          # optional
//	background = "#211f1d"
//	text       = "#cfc8bd"
//	primary    = "#cf7a45"
//	...
//
// The file is read again each time the theme is activated (`:theme <name>`),
// so a tool that generates it can change colors without restarting vimail.

// userThemeKeys maps the file's keys to the role each one colors.
var userThemeKeys = map[string]func(Theme) lipgloss.Color{
	"primary":              Theme.Primary,
	"secondary":            Theme.Secondary,
	"accent":               Theme.Accent,
	"error":                Theme.Error,
	"warning":              Theme.Warning,
	"success":              Theme.Success,
	"info":                 Theme.Info,
	"text":                 Theme.Text,
	"text_muted":           Theme.TextMuted,
	"text_emphasized":      Theme.TextEmphasized,
	"background":           Theme.Background,
	"background_secondary": Theme.BackgroundSecondary,
	"background_darker":    Theme.BackgroundDarker,
	"border_normal":        Theme.BorderNormal,
	"border_focused":       Theme.BorderFocused,
	"border_dim":           Theme.BorderDim,
	"selection":            Theme.Selection,
	"selection_text":       Theme.SelectionText,
	"normal_mode":          Theme.NormalMode,
	"insert_mode":          Theme.InsertMode,
	"visual_mode":          Theme.VisualMode,
	"command_mode":         Theme.CommandMode,
}

// UserThemeKeys lists the color keys a theme file may set, sorted.
func UserThemeKeys() []string {
	keys := make([]string, 0, len(userThemeKeys))
	for k := range userThemeKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var userThemeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type userTheme struct {
	name string
	path string

	mu     sync.RWMutex
	base   Theme
	colors map[string]lipgloss.Color
}

// LoadUserThemes registers every <name>.toml in dir as a theme. It returns
// one error per problem (a bad file name, an unreadable file, an invalid
// color); a theme with problems is still registered with what could be read,
// so one typo does not leave vimail without the theme.
func LoadUserThemes(dir string) []error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no themes directory: nothing to load
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".toml")
		if !userThemeNameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("theme file %q: name may only contain a-z, 0-9, - and _", e.Name()))
			continue
		}
		if existing, ok := registry[name]; ok {
			if _, isUser := existing.(*userTheme); !isUser {
				errs = append(errs, fmt.Errorf("theme file %q: %q is a built-in theme; choose another name", e.Name(), name))
				continue
			}
		}
		t := &userTheme{name: name, path: filepath.Join(dir, e.Name())}
		for _, err := range t.load() {
			errs = append(errs, fmt.Errorf("theme %q: %w", name, err))
		}
		Register(t)
	}
	return errs
}

// load reads the file. On an unreadable file the previous colors are kept.
func (t *userTheme) load() []error {
	var raw map[string]any
	if _, err := toml.DecodeFile(t.path, &raw); err != nil {
		return []error{err}
	}
	var errs []error
	base := registry["vimail"]
	colors := make(map[string]lipgloss.Color, len(raw))
	for key, value := range raw {
		s, isString := value.(string)
		switch {
		case key == "base":
			b, ok := registry[s]
			if _, isUser := b.(*userTheme); !isString || !ok || isUser {
				errs = append(errs, fmt.Errorf("base %v is not a built-in theme", value))
				continue
			}
			base = b
		case userThemeKeys[key] == nil:
			errs = append(errs, fmt.Errorf("unknown key %q", key))
		case !isString || !hexColorRe.MatchString(s):
			// Only #rrggbb: the value ends up in terminal color sequences.
			errs = append(errs, fmt.Errorf("%s = %v is not a #rrggbb color", key, value))
		default:
			colors[key] = lipgloss.Color(s)
		}
	}
	t.mu.Lock()
	t.base, t.colors = base, colors
	t.mu.Unlock()
	return errs
}

// Refresh re-reads the file; called by SetCurrent on activation.
func (t *userTheme) Refresh() { t.load() }

func (t *userTheme) color(key string) lipgloss.Color {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if c, ok := t.colors[key]; ok {
		return c
	}
	base := t.base
	if base == nil {
		base = registry["vimail"]
	}
	return userThemeKeys[key](base)
}

func (t *userTheme) Name() string { return t.name }

func (t *userTheme) Primary() lipgloss.Color             { return t.color("primary") }
func (t *userTheme) Secondary() lipgloss.Color           { return t.color("secondary") }
func (t *userTheme) Accent() lipgloss.Color              { return t.color("accent") }
func (t *userTheme) Error() lipgloss.Color               { return t.color("error") }
func (t *userTheme) Warning() lipgloss.Color             { return t.color("warning") }
func (t *userTheme) Success() lipgloss.Color             { return t.color("success") }
func (t *userTheme) Info() lipgloss.Color                { return t.color("info") }
func (t *userTheme) Text() lipgloss.Color                { return t.color("text") }
func (t *userTheme) TextMuted() lipgloss.Color           { return t.color("text_muted") }
func (t *userTheme) TextEmphasized() lipgloss.Color      { return t.color("text_emphasized") }
func (t *userTheme) Background() lipgloss.Color          { return t.color("background") }
func (t *userTheme) BackgroundSecondary() lipgloss.Color { return t.color("background_secondary") }
func (t *userTheme) BackgroundDarker() lipgloss.Color    { return t.color("background_darker") }
func (t *userTheme) BorderNormal() lipgloss.Color        { return t.color("border_normal") }
func (t *userTheme) BorderFocused() lipgloss.Color       { return t.color("border_focused") }
func (t *userTheme) BorderDim() lipgloss.Color           { return t.color("border_dim") }
func (t *userTheme) Selection() lipgloss.Color           { return t.color("selection") }
func (t *userTheme) SelectionText() lipgloss.Color       { return t.color("selection_text") }
func (t *userTheme) NormalMode() lipgloss.Color          { return t.color("normal_mode") }
func (t *userTheme) InsertMode() lipgloss.Color          { return t.color("insert_mode") }
func (t *userTheme) VisualMode() lipgloss.Color          { return t.color("visual_mode") }
func (t *userTheme) CommandMode() lipgloss.Color         { return t.color("command_mode") }
