package theme

import "github.com/charmbracelet/lipgloss"

type kanagawaTheme struct{ BaseTheme }

func init() { Register(kanagawaTheme{}) }

func (kanagawaTheme) Name() string { return "kanagawa" }

func (kanagawaTheme) Primary() lipgloss.Color           { return lipgloss.Color("#7e9cd8") }
func (kanagawaTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#957fb8") }
func (kanagawaTheme) Accent() lipgloss.Color             { return lipgloss.Color("#7fb4ca") }
func (kanagawaTheme) Text() lipgloss.Color               { return lipgloss.Color("#dcd7ba") }
func (kanagawaTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#727169") }
func (kanagawaTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#c8c093") }
func (kanagawaTheme) Background() lipgloss.Color          { return lipgloss.Color("#1f1f28") }
func (kanagawaTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#2a2a37") }
func (kanagawaTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#16161d") }
func (kanagawaTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#54546d") }
func (kanagawaTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#7e9cd8") }
func (kanagawaTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#2a2a37") }
func (kanagawaTheme) Selection() lipgloss.Color           { return lipgloss.Color("#2D4F67") }
func (kanagawaTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#dcd7ba") }

func (kanagawaTheme) Error() lipgloss.Color   { return lipgloss.Color("#e82424") }
func (kanagawaTheme) Warning() lipgloss.Color { return lipgloss.Color("#ff9e3b") }
func (kanagawaTheme) Success() lipgloss.Color { return lipgloss.Color("#76946a") }
func (kanagawaTheme) Info() lipgloss.Color    { return lipgloss.Color("#7fb4ca") }

func (kanagawaTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#7e9cd8") }
func (kanagawaTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#76946a") }
func (kanagawaTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#ff9e3b") }
func (kanagawaTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#957fb8") }
