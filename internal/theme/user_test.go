package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func writeTheme(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUserThemeOverridesAndFallsBackToBase(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "desk.toml", "base = \"gruvbox\"\nprimary = \"#cf7a45\"\nbackground = \"#211F1D\"\n")
	if errs := LoadUserThemes(dir); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	defer delete(registry, "desk")

	th := Get("desk")
	if th.Name() != "desk" || th.Primary() != lipgloss.Color("#cf7a45") || th.Background() != lipgloss.Color("#211F1D") {
		t.Fatalf("overrides not applied: %v %v %v", th.Name(), th.Primary(), th.Background())
	}
	if th.Text() != Get("gruvbox").Text() || th.Selection() != Get("gruvbox").Selection() {
		t.Fatal("missing keys should come from the base theme")
	}
	found := false
	for _, n := range Names() {
		found = found || n == "desk"
	}
	if !found {
		t.Fatal("user theme not listed in Names()")
	}
}

func TestUserThemeRejectsAnythingButHexColors(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "bad.toml", "primary = \"\\u001b]52;c;QUJD\\u0007\"\ntext = \"red\"\nselection = 5\nbogus = \"#000000\"\nbase = \"nope\"\naccent = \"#abcdef\"\n")
	errs := LoadUserThemes(dir)
	defer delete(registry, "bad")
	if len(errs) != 5 {
		t.Fatalf("got %d errors, want 5: %v", len(errs), errs)
	}
	th := Get("bad")
	// The theme still loads: valid keys apply, invalid ones fall back.
	if th.Accent() != lipgloss.Color("#abcdef") {
		t.Fatal("valid key was dropped")
	}
	if th.Primary() != Get("vimail").Primary() || th.Text() != Get("vimail").Text() {
		t.Fatal("invalid values must fall back to the base")
	}
	for _, key := range UserThemeKeys() {
		if c := string(userThemeKeys[key](th)); strings.ContainsAny(c, "\x1b\x07") {
			t.Fatalf("%s carries an escape sequence: %q", key, c)
		}
	}
}

func TestUserThemeNamesAndBuiltIns(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "nord.toml", "primary = \"#000000\"\n")     // built-in name
	writeTheme(t, dir, "Bad Name.toml", "primary = \"#000000\"\n") // invalid name
	writeTheme(t, dir, "notes.txt", "not a theme")                 // ignored
	nordBefore := Get("nord").Primary()
	if errs := LoadUserThemes(dir); len(errs) != 2 {
		t.Fatalf("got %d errors, want 2: %v", len(errs), errs)
	}
	if Get("nord").Primary() != nordBefore {
		t.Fatal("a file must not replace a built-in theme")
	}
	if errs := LoadUserThemes(filepath.Join(dir, "missing")); errs != nil {
		t.Fatalf("missing directory should be fine, got %v", errs)
	}
}

func TestUserThemeIsReReadOnActivation(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "live.toml", "primary = \"#111111\"\n")
	LoadUserThemes(dir)
	defer delete(registry, "live")
	old := currentTheme
	defer func() { currentTheme = old }()

	SetCurrent("live")
	if Current().Primary() != lipgloss.Color("#111111") {
		t.Fatal("initial color wrong")
	}
	writeTheme(t, dir, "live.toml", "primary = \"#222222\"\n")
	SetCurrent("live") // `:theme live` again
	if Current().Primary() != lipgloss.Color("#222222") {
		t.Fatal("file change not picked up on activation")
	}
}
