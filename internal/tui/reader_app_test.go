package tui

import (
	"testing"

	"github.com/gausejakub/vimail/internal/tui/keys"
	"github.com/gausejakub/vimail/internal/tui/layout"
	"github.com/gausejakub/vimail/pkg/vimtea"
)

func TestVInPreviewPaneOpensReaderThenSelects(t *testing.T) {
	m := testApp()
	send(&m, "l")
	if m.focusedPane != layout.PanePreview {
		t.Fatalf("focus = %v, want preview", m.focusedPane)
	}
	send(&m, "v")
	if !m.preview.Reading() {
		t.Fatal("v in the preview pane did not open the reader")
	}
	if m.mode == keys.ModeVisual {
		t.Fatal("v also started the message-list visual mode")
	}
	// The opening v only shows the cursor; the next v starts the selection there.
	if got := m.preview.ReaderMode(); got != vimtea.ModeNormal {
		t.Fatalf("reader mode = %v, want normal after the opening v", got)
	}
	send(&m, "v")
	if got := m.preview.ReaderMode(); got != vimtea.ModeVisual {
		t.Fatalf("reader mode = %v, want visual after the second v", got)
	}
	send(&m, "esc") // ends the selection
	send(&m, "q")   // closes the reader
	if m.preview.Reading() {
		t.Fatal("q did not close the reader")
	}
}

func TestSlashInPreviewPaneSearchesInReader(t *testing.T) {
	m := testApp()
	send(&m, "l")
	send(&m, "/")
	if !m.preview.Reading() || m.cmdActive {
		t.Fatalf("reading=%v cmdActive=%v, want reader search, not global search", m.preview.Reading(), m.cmdActive)
	}
}

func TestVimKeysElsewhereKeepTheirMeaning(t *testing.T) {
	m := testApp() // focus: message list
	send(&m, "v")
	if m.preview.Reading() {
		t.Fatal("v in the message list opened the reader")
	}
	if m.mode != keys.ModeVisual {
		t.Fatal("v in the message list should start list visual mode")
	}
}

func TestEnterOnLoadedMessageOpensReader(t *testing.T) {
	m := testApp() // mock store: messages have bodies
	send(&m, "enter")
	if !m.preview.Reading() || m.focusedPane != layout.PanePreview {
		t.Fatalf("reading=%v focus=%v, want reader in the preview pane", m.preview.Reading(), m.focusedPane)
	}
}
