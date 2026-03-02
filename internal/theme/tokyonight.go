package theme

import "github.com/charmbracelet/lipgloss"

type tokyonightTheme struct{ BaseTheme }

func init() { Register(tokyonightTheme{}) }

func (tokyonightTheme) Name() string { return "tokyonight" }

func (tokyonightTheme) Primary() lipgloss.Color           { return lipgloss.Color("#7aa2f7") }
func (tokyonightTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#bb9af7") }
func (tokyonightTheme) Accent() lipgloss.Color             { return lipgloss.Color("#7dcfff") }
func (tokyonightTheme) Text() lipgloss.Color               { return lipgloss.Color("#c0caf5") }
func (tokyonightTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#565f89") }
func (tokyonightTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#c0caf5") }
func (tokyonightTheme) Background() lipgloss.Color          { return lipgloss.Color("#1a1b26") }
func (tokyonightTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#24283b") }
func (tokyonightTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#16161e") }
func (tokyonightTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#3b4261") }
func (tokyonightTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#7aa2f7") }
func (tokyonightTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#292e42") }
func (tokyonightTheme) Selection() lipgloss.Color           { return lipgloss.Color("#33467C") }
func (tokyonightTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#c0caf5") }

func (tokyonightTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#7aa2f7") }
func (tokyonightTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#9ece6a") }
func (tokyonightTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#e0af68") }
func (tokyonightTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#bb9af7") }
