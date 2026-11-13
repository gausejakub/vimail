package tui

import (
	"os"
	"testing"

	"github.com/gausejakub/vimail/internal/tui/components/preview"
)

// Tests in this package drive the whole TUI with real key presses, including
// "o" (open in browser) and "y" in the reader (copy). They must never launch
// a browser or overwrite the clipboard on the machine running them, so both
// are replaced for the whole test binary.
func TestMain(m *testing.M) {
	preview.OpenExternal = func(string) error { return nil }
	writeClipboard = func(string) error { return nil }
	code := m.Run()
	preview.CleanupTemp()
	os.Exit(code)
}
