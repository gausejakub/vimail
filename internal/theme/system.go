package theme

import "github.com/charmbracelet/lipgloss"

type systemTheme struct{ BaseTheme }

func init() { Register(systemTheme{}) }

func (systemTheme) Name() string { return "system" }

// ANSI 16-color only — no hex codes.
func (systemTheme) Primary() lipgloss.Color           { return lipgloss.Color("4") }  // blue
func (systemTheme) Secondary() lipgloss.Color          { return lipgloss.Color("6") }  // cyan
func (systemTheme) Accent() lipgloss.Color             { return lipgloss.Color("14") } // bright cyan
func (systemTheme) Text() lipgloss.Color               { return lipgloss.Color("7") }  // white
func (systemTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("8") }  // bright black
func (systemTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("15") } // bright white
func (systemTheme) Background() lipgloss.Color          { return lipgloss.Color("0") }  // black
func (systemTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("0") }
func (systemTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("0") }
func (systemTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("8") }
func (systemTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("4") }
func (systemTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("0") }
func (systemTheme) Selection() lipgloss.Color           { return lipgloss.Color("4") }  // blue bg
func (systemTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("15") } // bright white

func (systemTheme) Error() lipgloss.Color   { return lipgloss.Color("1") }  // red
func (systemTheme) Warning() lipgloss.Color { return lipgloss.Color("3") }  // yellow
func (systemTheme) Success() lipgloss.Color { return lipgloss.Color("2") }  // green
func (systemTheme) Info() lipgloss.Color    { return lipgloss.Color("6") }  // cyan

func (systemTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("4") }  // blue
func (systemTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("2") }  // green
func (systemTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("3") }  // yellow
func (systemTheme) CommandMode() lipgloss.Color { return lipgloss.Color("5") }  // magenta
