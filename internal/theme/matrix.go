package theme

import "github.com/charmbracelet/lipgloss"

type matrixTheme struct{ BaseTheme }

func init() { Register(matrixTheme{}) }

func (matrixTheme) Name() string { return "matrix" }

func (matrixTheme) Primary() lipgloss.Color           { return lipgloss.Color("#00FF41") }
func (matrixTheme) Secondary() lipgloss.Color          { return lipgloss.Color("#008F11") }
func (matrixTheme) Accent() lipgloss.Color             { return lipgloss.Color("#00FF41") }
func (matrixTheme) Text() lipgloss.Color               { return lipgloss.Color("#00FF41") }
func (matrixTheme) TextMuted() lipgloss.Color           { return lipgloss.Color("#005500") }
func (matrixTheme) TextEmphasized() lipgloss.Color      { return lipgloss.Color("#33FF66") }
func (matrixTheme) Background() lipgloss.Color          { return lipgloss.Color("#000000") }
func (matrixTheme) BackgroundSecondary() lipgloss.Color { return lipgloss.Color("#0D0D0D") }
func (matrixTheme) BackgroundDarker() lipgloss.Color    { return lipgloss.Color("#000000") }
func (matrixTheme) BorderNormal() lipgloss.Color        { return lipgloss.Color("#003B00") }
func (matrixTheme) BorderFocused() lipgloss.Color       { return lipgloss.Color("#00FF41") }
func (matrixTheme) BorderDim() lipgloss.Color           { return lipgloss.Color("#001A00") }
func (matrixTheme) Selection() lipgloss.Color           { return lipgloss.Color("#003B00") }
func (matrixTheme) SelectionText() lipgloss.Color       { return lipgloss.Color("#33FF66") }

func (matrixTheme) Error() lipgloss.Color   { return lipgloss.Color("#FF0000") }
func (matrixTheme) Warning() lipgloss.Color { return lipgloss.Color("#FFFF00") }
func (matrixTheme) Success() lipgloss.Color { return lipgloss.Color("#00FF41") }
func (matrixTheme) Info() lipgloss.Color    { return lipgloss.Color("#00FF41") }

func (matrixTheme) NormalMode() lipgloss.Color  { return lipgloss.Color("#00FF41") }
func (matrixTheme) InsertMode() lipgloss.Color  { return lipgloss.Color("#33FF66") }
func (matrixTheme) VisualMode() lipgloss.Color  { return lipgloss.Color("#FFFF00") }
func (matrixTheme) CommandMode() lipgloss.Color { return lipgloss.Color("#008F11") }
