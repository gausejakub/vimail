package theme

import "github.com/charmbracelet/lipgloss"

type gruvboxTheme struct{ BaseTheme }

func init() { Register(gruvboxTheme{}) }

func (gruvboxTheme) Name() string { return "gruvbox" }

func (gruvboxTheme) Primary() lipgloss.Color           { return lipgloss.Color("#458588") }
func (gruvboxTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#b8bb26") }
func (gruvboxTheme) Accent() lipgloss.Color             { return lipgloss.Color("#fabd2f") }
func (gruvboxTheme) Text() lipgloss.Color               { return lipgloss.Color("#ebdbb2") }
func (gruvboxTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#928374") }
func (gruvboxTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#fbf1c7") }
func (gruvboxTheme) Background() lipgloss.Color          { return lipgloss.Color("#282828") }
func (gruvboxTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#3c3836") }
func (gruvboxTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#1d2021") }
func (gruvboxTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#504945") }
func (gruvboxTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#458588") }
func (gruvboxTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#3c3836") }
func (gruvboxTheme) Selection() lipgloss.Color           { return lipgloss.Color("#504945") }
func (gruvboxTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#fbf1c7") }

func (gruvboxTheme) Error() lipgloss.Color   { return lipgloss.Color("#fb4934") }
func (gruvboxTheme) Warning() lipgloss.Color { return lipgloss.Color("#fabd2f") }
func (gruvboxTheme) Success() lipgloss.Color { return lipgloss.Color("#b8bb26") }
func (gruvboxTheme) Info() lipgloss.Color    { return lipgloss.Color("#83a598") }

func (gruvboxTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#83a598") }
func (gruvboxTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#b8bb26") }
func (gruvboxTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#fabd2f") }
func (gruvboxTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#d3869b") }
