package layout

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gausejakub/vimail/internal/theme"
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
	if c.Border == BorderNone {
		return content
	}

	t := theme.Current()

	border := lipgloss.RoundedBorder()
	if c.Border == BorderNormal {
		border = lipgloss.NormalBorder()
	}

	borderColor := t.BorderDim()
	if c.Focused {
		borderColor = t.BorderFocused()
	}

	bStyle := lipgloss.NewStyle().Foreground(borderColor)
	innerW := c.InnerWidth()
	innerH := c.InnerHeight()

	// Split content into lines. Do NOT let lipgloss re-measure or re-wrap
	// ANSI-styled content — that causes column corruption. Instead, manually
	// construct the bordered output with exact dimensions.
	lines := strings.Split(content, "\n")

	emptyLine := fmt.Sprintf("%*s", innerW, "")

	var b strings.Builder

	// Top border: ╭────╮
	b.WriteString(bStyle.Render(border.TopLeft + strings.Repeat(border.Top, innerW) + border.TopRight))

	// Content lines: │content│
	for i := 0; i < innerH; i++ {
		b.WriteByte('\n')
		b.WriteString(bStyle.Render(border.Left))
		if i < len(lines) {
			line := lines[i]
			// Enforce exact visible width: pad short lines, truncate long ones.
			visW := lipgloss.Width(line)
			if visW < innerW {
				line += fmt.Sprintf("%*s", innerW-visW, "")
			} else if visW > innerW {
				line = lipgloss.NewStyle().MaxWidth(innerW).Render(line)
			}
			b.WriteString(line)
		} else {
			b.WriteString(emptyLine)
		}
		b.WriteString(bStyle.Render(border.Right))
	}

	// Bottom border: ╰────╯
	b.WriteByte('\n')
	b.WriteString(bStyle.Render(border.BottomLeft + strings.Repeat(border.Bottom, innerW) + border.BottomRight))

	return b.String()
}
