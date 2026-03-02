package theme

import "github.com/charmbracelet/lipgloss"

// Theme defines the color palette interface for vmail.
// All methods return lipgloss.Color — components compose their own styles.
type Theme interface {
	Name() string

	Primary() lipgloss.Color
	Secondary() lipgloss.Color
	Accent() lipgloss.Color
	Error() lipgloss.Color
	Warning() lipgloss.Color
	Success() lipgloss.Color
	Info() lipgloss.Color

	Text() lipgloss.Color
	TextMuted() lipgloss.Color
	TextEmphasized() lipgloss.Color

	Background() lipgloss.Color
	BackgroundSecondary() lipgloss.Color
	BackgroundDarker() lipgloss.Color

	BorderNormal() lipgloss.Color
	BorderFocused() lipgloss.Color
	BorderDim() lipgloss.Color

	Selection() lipgloss.Color    // cursor/selection row background
	SelectionText() lipgloss.Color // text on selection row

	NormalMode() lipgloss.Color
	InsertMode() lipgloss.Color
	VisualMode() lipgloss.Color
	CommandMode() lipgloss.Color
}

// BaseTheme provides sensible defaults for semantic colors.
// Embed this in concrete themes and override what you need.
type BaseTheme struct{}

func (BaseTheme) Error() lipgloss.Color   { return lipgloss.Color("#E06C75") }
func (BaseTheme) Warning() lipgloss.Color { return lipgloss.Color("#E5C07B") }
func (BaseTheme) Success() lipgloss.Color { return lipgloss.Color("#98C379") }
func (BaseTheme) Info() lipgloss.Color    { return lipgloss.Color("#61AFEF") }

func (BaseTheme) Selection() lipgloss.Color     { return lipgloss.Color("#3E4451") }
func (BaseTheme) SelectionText() lipgloss.Color { return lipgloss.Color("#FFFFFF") }

func (BaseTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#61AFEF") }
func (BaseTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#98C379") }
func (BaseTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#E5C07B") }
func (BaseTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#C678DD") }

// Registry

var (
	registry     = map[string]Theme{}
	currentTheme Theme
)

func Register(t Theme) {
	registry[t.Name()] = t
}

func Get(name string) Theme {
	if t, ok := registry[name]; ok {
		return t
	}
	return registry["vmail"]
}

func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}

func SetCurrent(name string) {
	currentTheme = Get(name)
}

func Current() Theme {
	if currentTheme == nil {
		return Get("vmail")
	}
	return currentTheme
}
