package theme

import "github.com/charmbracelet/lipgloss"

type vmailTheme struct{ BaseTheme }

func init() { Register(vmailTheme{}) }

func (vmailTheme) Name() string { return "vmail" }

func (vmailTheme) Primary() lipgloss.Color           { return lipgloss.Color("#88C0D0") }
func (vmailTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#81A1C1") }
func (vmailTheme) Accent() lipgloss.Color             { return lipgloss.Color("#5E81AC") }
func (vmailTheme) Text() lipgloss.Color               { return lipgloss.Color("#D8DEE9") }
func (vmailTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#7B88A1") }
func (vmailTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#ECEFF4") }
func (vmailTheme) Background() lipgloss.Color          { return lipgloss.Color("#1E2128") }
func (vmailTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#282C34") }
func (vmailTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#16191F") }
func (vmailTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#3B4252") }
func (vmailTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#88C0D0") }
func (vmailTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#2E3440") }
func (vmailTheme) Selection() lipgloss.Color           { return lipgloss.Color("#3B4252") }
func (vmailTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#ECEFF4") }
