package theme

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// relativeLuminance implements the WCAG 2.x formula for sRGB hex colors.
func relativeLuminance(hex string) float64 {
	channel := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(v) / 255.0
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(1) + 0.7152*channel(3) + 0.0722*channel(5)
}

func contrastRatio(a, b lipgloss.Color) float64 {
	la, lb := relativeLuminance(string(a)), relativeLuminance(string(b))
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func isHex(c lipgloss.Color) bool {
	s := string(c)
	return strings.HasPrefix(s, "#") && len(s) == 7
}

// TestThemeContrast is a WCAG gate for every registered truecolor theme:
// body text must reach AA for normal text (4.5:1) against the background,
// and status colors must reach AA for large text/UI components (3:1).
// ANSI-indexed colors (system, omarchy fallback) are terminal-defined and
// are skipped.
func TestThemeContrast(t *testing.T) {
	for name, th := range registry {
		bg := th.Background()
		if !isHex(bg) {
			continue
		}
		check := func(role string, fg lipgloss.Color, min float64) {
			if !isHex(fg) {
				return
			}
			if r := contrastRatio(fg, bg); r < min {
				t.Errorf("%s: %s %s has contrast %.2f:1 against background %s, want >= %.1f:1",
					name, role, fg, r, bg, min)
			}
		}
		check("Text", th.Text(), 4.5)
		check("TextEmphasized", th.TextEmphasized(), 4.5)
		check("Primary", th.Primary(), 3.0)
		check("Error", th.Error(), 3.0)
		check("Warning", th.Warning(), 3.0)
		check("Success", th.Success(), 3.0)
		check("Info", th.Info(), 3.0)

		// Selected-row text sits on the selection background, not the theme bg.
		if isHex(th.SelectionText()) && isHex(th.Selection()) {
			if r := contrastRatio(th.SelectionText(), th.Selection()); r < 4.5 {
				t.Errorf("%s: SelectionText %s on Selection %s has contrast %.2f:1, want >= 4.5:1",
					name, th.SelectionText(), th.Selection(), r)
			}
		}
	}
}

func TestOmarchyThemeLoadsPalette(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "colors.toml")
	fixture := `mode = "dark"
background = "#1a1b26"
lighter_background = "#24283b"
foreground = "#a9b1d6"
bright_foreground = "#c0caf5"
accent = "#7aa2f7"
selection = "#292e42"
muted = "#414868"
red = "#f7768e"
green = "#9ece6a"
yellow = "#e0af68"
blue = "#7aa2f7"
magenta = "#ad8ee6"
cyan = "#449dab"
`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := omarchyColorsFile
	defer func() { omarchyColorsFile = orig; Get("omarchy").(*omarchyTheme).Refresh() }()
	omarchyColorsFile = func() string { return path }

	// SetCurrent triggers Refresh via the refresher interface.
	SetCurrent("omarchy")
	th := Current()

	if got := th.Primary(); got != lipgloss.Color("#7aa2f7") {
		t.Errorf("Primary = %s, want #7aa2f7 (accent)", got)
	}
	if got := th.Background(); got != lipgloss.Color("#1a1b26") {
		t.Errorf("Background = %s, want #1a1b26", got)
	}
	if got := th.Selection(); got != lipgloss.Color("#292e42") {
		t.Errorf("Selection = %s, want #292e42", got)
	}
	if got := th.TextMuted(); got != lipgloss.Color("#414868") {
		t.Errorf("TextMuted = %s, want #414868 (muted)", got)
	}
	// darker_background is absent from the fixture: falls back to background.
	if got := th.BackgroundDarker(); got != lipgloss.Color("#1a1b26") {
		t.Errorf("BackgroundDarker = %s, want fallback #1a1b26", got)
	}
}

func TestOmarchyThemeANSIFallback(t *testing.T) {
	orig := omarchyColorsFile
	defer func() { omarchyColorsFile = orig; Get("omarchy").(*omarchyTheme).Refresh() }()
	omarchyColorsFile = func() string { return filepath.Join(t.TempDir(), "missing.toml") }

	SetCurrent("omarchy")
	th := Current()

	if got := th.Primary(); got != lipgloss.Color("4") {
		t.Errorf("Primary = %s, want ANSI 4", got)
	}
	if got := th.Text(); got != lipgloss.Color("7") {
		t.Errorf("Text = %s, want ANSI 7", got)
	}
	if got := th.Background(); got != lipgloss.Color("0") {
		t.Errorf("Background = %s, want ANSI 0", got)
	}
}
