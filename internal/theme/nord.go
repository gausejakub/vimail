package theme

import "github.com/charmbracelet/lipgloss"

type nordTheme struct{ BaseTheme }

func init() { Register(nordTheme{}) }

func (nordTheme) Name() string { return "nord" }

func (nordTheme) Primary() lipgloss.Color           { return lipgloss.Color("#5e81ac") }
func (nordTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#81a1c1") }
func (nordTheme) Accent() lipgloss.Color             { return lipgloss.Color("#88c0d0") }
func (nordTheme) Text() lipgloss.Color               { return lipgloss.Color("#d8dee9") }
func (nordTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#4c566a") }
func (nordTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#eceff4") }
func (nordTheme) Background() lipgloss.Color          { return lipgloss.Color("#2e3440") }
func (nordTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#3b4252") }
func (nordTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#242933") }
func (nordTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#434c5e") }
func (nordTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#88c0d0") }
func (nordTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#3b4252") }
func (nordTheme) Selection() lipgloss.Color           { return lipgloss.Color("#434c5e") }
func (nordTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#eceff4") }

func (nordTheme) Error() lipgloss.Color   { return lipgloss.Color("#bf616a") }
func (nordTheme) Warning() lipgloss.Color { return lipgloss.Color("#ebcb8b") }
func (nordTheme) Success() lipgloss.Color { return lipgloss.Color("#a3be8c") }
func (nordTheme) Info() lipgloss.Color    { return lipgloss.Color("#88c0d0") }

func (nordTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#5e81ac") }
func (nordTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#a3be8c") }
func (nordTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#ebcb8b") }
func (nordTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#b48ead") }
