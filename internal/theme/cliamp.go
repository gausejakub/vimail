package theme

import "github.com/charmbracelet/lipgloss"

// cliampTheme is modeled on cliamp's signature "winamp" palette
// (Winamp 2.91 PLEDIT/VISCOLOR colors): pure black background, hot
// green accent, grey muted text, VU-meter green/yellow/red status hues.
type cliampTheme struct{ BaseTheme }

func init() { Register(cliampTheme{}) }

func (cliampTheme) Name() string { return "cliamp" }

func (cliampTheme) Primary() lipgloss.Color             { return lipgloss.Color("#00FF00") }
func (cliampTheme) Secondary() lipgloss.Color           { return lipgloss.Color("#29CE10") }
func (cliampTheme) Accent() lipgloss.Color              { return lipgloss.Color("#D6B521") }
func (cliampTheme) Text() lipgloss.Color                { return lipgloss.Color("#FFFFFF") }
func (cliampTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#969696") }
func (cliampTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#FFFFFF") }
func (cliampTheme) Background() lipgloss.Color          { return lipgloss.Color("#000000") }
func (cliampTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#0A0A0A") }
func (cliampTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#000000") }
func (cliampTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#333333") }
func (cliampTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#00FF00") }
func (cliampTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#1A1A1A") }

// Selection mirrors cliamp's key pills: accent background with
// luminance-contrasting black text (green is bright, so text is black).
func (cliampTheme) Selection() lipgloss.Color     { return lipgloss.Color("#00FF00") }
func (cliampTheme) SelectionText() lipgloss.Color { return lipgloss.Color("#000000") }

func (cliampTheme) Error() lipgloss.Color   { return lipgloss.Color("#EF3110") }
func (cliampTheme) Warning() lipgloss.Color { return lipgloss.Color("#D6B521") }
func (cliampTheme) Success() lipgloss.Color { return lipgloss.Color("#29CE10") }
func (cliampTheme) Info() lipgloss.Color    { return lipgloss.Color("#00FF00") }

func (cliampTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#00FF00") }
func (cliampTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#FFFFFF") }
func (cliampTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#D6B521") }
func (cliampTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#EF3110") }
