package layout

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PlaceOverlay composites the foreground string onto the background at (x, y).
// This is ANSI-aware — it respects escape sequences.
func PlaceOverlay(x, y int, fg, bg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	for i, fgLine := range fgLines {
		bgIdx := y + i
		if bgIdx < 0 || bgIdx >= len(bgLines) {
			continue
		}

		bgLine := bgLines[bgIdx]
		bgWidth := lipgloss.Width(bgLine)
		fgWidth := lipgloss.Width(fgLine)

		if x >= bgWidth {
			bgLines[bgIdx] = bgLine + strings.Repeat(" ", x-bgWidth) + fgLine
			continue
		}

		// Build: prefix + fg + suffix
		prefix := truncateAnsi(bgLine, x)
		suffixStart := x + fgWidth
		suffix := ""
		if suffixStart < bgWidth {
			suffix = cutLeftAnsi(bgLine, suffixStart)
		}

		bgLines[bgIdx] = prefix + fgLine + suffix
	}

	return strings.Join(bgLines, "\n")
}

// CenterOverlay returns the (x, y) to center fg on a bg of given dimensions.
func CenterOverlay(bgW, bgH int, fg string) (int, int) {
	fgW := lipgloss.Width(fg)
	fgH := lipgloss.Height(fg)
	x := max(0, (bgW-fgW)/2)
	y := max(0, (bgH-fgH)/2)
	return x, y
}

// PlaceOverlayCentered composites fg centered on bg.
func PlaceOverlayCentered(fg, bg string, bgW, bgH int) string {
	x, y := CenterOverlay(bgW, bgH, fg)
	return PlaceOverlay(x, y, fg, bg)
}

// truncateAnsi returns the first n visible characters of s, preserving ANSI.
func truncateAnsi(s string, n int) string {
	if n <= 0 {
		return ""
	}
	// Use lipgloss to measure and cut
	w := lipgloss.Width(s)
	if n >= w {
		return s
	}
	// Simple approach: render at fixed width and trim
	return lipgloss.NewStyle().MaxWidth(n).Render(s)
}

// cutLeftAnsi returns s with the first n visible characters removed.
func cutLeftAnsi(s string, n int) string {
	w := lipgloss.Width(s)
	if n >= w {
		return ""
	}
	// Render at reduced max width by padding and cutting
	// Simple: use lipgloss.Width to measure, then cut rune by rune
	visible := 0
	byteIdx := 0
	inEscape := false
	for i, r := range s {
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
				inEscape = false
			}
			byteIdx = i + len(string(r))
			continue
		}
		if r == '\x1b' {
			inEscape = true
			byteIdx = i + 1
			continue
		}
		if visible >= n {
			return s[i:]
		}
		visible++
		byteIdx = i + len(string(r))
	}
	_ = byteIdx
	return ""
}
