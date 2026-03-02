package layout

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/gause/vmail/internal/theme"
)

type BorderStyle int

const (
	BorderNone BorderStyle = iota
	BorderNormal
	BorderRounded
)

type Container struct {
	Width   int
	Height  int
	Focused bool
	Border  BorderStyle
}

func (c Container) InnerWidth() int {
	if c.Border == BorderNone {
		return c.Width
	}
	return max(0, c.Width-2)
}

func (c Container) InnerHeight() int {
	if c.Border == BorderNone {
		return c.Height
	}
	return max(0, c.Height-2)
}

func (c Container) Render(content string) string {
	t := theme.Current()

	if c.Border == BorderNone {
		return lipgloss.NewStyle().
			Width(c.Width).
			Height(c.Height).
			Render(content)
	}

	border := lipgloss.RoundedBorder()
	if c.Border == BorderNormal {
		border = lipgloss.NormalBorder()
	}

	borderColor := t.BorderDim()
	if c.Focused {
		borderColor = t.BorderFocused()
	}

	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(borderColor).
		Width(c.InnerWidth()).
		Height(c.InnerHeight()).
		Render(content)
}
