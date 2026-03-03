package theme

import "github.com/charmbracelet/lipgloss"

type vimailTheme struct{ BaseTheme }

func init() { Register(vimailTheme{}) }

func (vimailTheme) Name() string { return "vimail" }

func (vimailTheme) Primary() lipgloss.Color           { return lipgloss.Color("#88C0D0") }
func (vimailTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#81A1C1") }
func (vimailTheme) Accent() lipgloss.Color             { return lipgloss.Color("#5E81AC") }
func (vimailTheme) Text() lipgloss.Color               { return lipgloss.Color("#D8DEE9") }
func (vimailTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#7B88A1") }
func (vimailTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#ECEFF4") }
func (vimailTheme) Background() lipgloss.Color          { return lipgloss.Color("#1E2128") }
func (vimailTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#282C34") }
func (vimailTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#16191F") }
func (vimailTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#3B4252") }
func (vimailTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#88C0D0") }
func (vimailTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#2E3440") }
func (vimailTheme) Selection() lipgloss.Color           { return lipgloss.Color("#3B4252") }
func (vimailTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#ECEFF4") }
