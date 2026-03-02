package theme

import "github.com/charmbracelet/lipgloss"

type catppuccinTheme struct{ BaseTheme }

func init() { Register(catppuccinTheme{}) }

func (catppuccinTheme) Name() string { return "catppuccin" }

func (catppuccinTheme) Primary() lipgloss.Color           { return lipgloss.Color("#89b4fa") }
func (catppuccinTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#cba6f7") }
func (catppuccinTheme) Accent() lipgloss.Color             { return lipgloss.Color("#f5c2e7") }
func (catppuccinTheme) Text() lipgloss.Color               { return lipgloss.Color("#cdd6f4") }
func (catppuccinTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#6c7086") }
func (catppuccinTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#cdd6f4") }
func (catppuccinTheme) Background() lipgloss.Color          { return lipgloss.Color("#1e1e2e") }
func (catppuccinTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#313244") }
func (catppuccinTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#181825") }
func (catppuccinTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#45475a") }
func (catppuccinTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#89b4fa") }
func (catppuccinTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#313244") }
func (catppuccinTheme) Selection() lipgloss.Color           { return lipgloss.Color("#45475a") }
func (catppuccinTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#cdd6f4") }

func (catppuccinTheme) Error() lipgloss.Color   { return lipgloss.Color("#f38ba8") }
func (catppuccinTheme) Warning() lipgloss.Color { return lipgloss.Color("#fab387") }
func (catppuccinTheme) Success() lipgloss.Color { return lipgloss.Color("#a6e3a1") }
func (catppuccinTheme) Info() lipgloss.Color    { return lipgloss.Color("#89b4fa") }

func (catppuccinTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#89b4fa") }
func (catppuccinTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#a6e3a1") }
func (catppuccinTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#fab387") }
func (catppuccinTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#cba6f7") }
