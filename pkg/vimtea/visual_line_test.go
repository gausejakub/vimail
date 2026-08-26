package vimtea

import "testing"

// Regression tests for linewise visual deletion (issue #2): V..d must
// remove whole lines including their line breaks, store a linewise
// yank, and place the cursor as Vim does.

func TestVisualLineDelete(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		keys     []string
		wantText string
		wantYank string
		wantRow  int
		wantCol  int
	}{
		{
			name:     "Vd removes middle line",
			content:  "line1\nline2\nline3",
			keys:     []string{"j", "V", "d"},
			wantText: "line1\nline3",
			wantYank: "\nline2",
			wantRow:  1, wantCol: 0,
		},
		{
			name:     "Vd removes first line",
			content:  "line1\nline2\nline3",
			keys:     []string{"V", "d"},
			wantText: "line2\nline3",
			wantYank: "\nline1",
			wantRow:  0, wantCol: 0,
		},
		{
			name:     "Vd removes final line and moves cursor up",
			content:  "line1\nline2\nline3",
			keys:     []string{"G", "V", "d"},
			wantText: "line1\nline2",
			wantYank: "\nline3",
			wantRow:  1, wantCol: 0,
		},
		{
			name:     "Vjd removes a multi-line selection",
			content:  "line1\nline2\nline3\nline4",
			keys:     []string{"j", "V", "j", "d"},
			wantText: "line1\nline4",
			wantYank: "\nline2\nline3",
			wantRow:  1, wantCol: 0,
		},
		{
			name:     "Vkd removes selection made upward",
			content:  "line1\nline2\nline3",
			keys:     []string{"j", "j", "V", "k", "d"},
			wantText: "line1",
			wantYank: "\nline2\nline3",
			wantRow:  0, wantCol: 0,
		},
		{
			name:     "deleting every line leaves one empty buffer line",
			content:  "line1\nline2",
			keys:     []string{"V", "j", "d"},
			wantText: "",
			wantYank: "\nline1\nline2",
			wantRow:  0, wantCol: 0,
		},
		{
			name:     "Vd on indented follower rests on first non-blank",
			content:  "line1\n\tline2",
			keys:     []string{"V", "d"},
			wantText: "\tline2",
			wantYank: "\nline1",
			wantRow:  0, wantCol: 1,
		},
		{
			name:     "Vxd via x alias also deletes linewise",
			content:  "line1\nline2\nline3",
			keys:     []string{"j", "V", "x"},
			wantText: "line1\nline3",
			wantYank: "\nline2",
			wantRow:  1, wantCol: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testEditor(tt.content)
			sendKeys(m, tt.keys...)
			assertText(t, m, tt.wantText)
			assertYank(t, m, tt.wantYank)
			assertCursor(t, m, tt.wantRow, tt.wantCol)
			assertMode(t, m, ModeNormal)
		})
	}
}

func TestVisualLineDeleteThenPaste(t *testing.T) {
	t.Run("Vd then p pastes the line below (linewise)", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "V", "d", "p")
		assertText(t, m, "line2\nline1\nline3")
		assertCursor(t, m, 1, 0)
	})

	t.Run("Vd then P pastes the line above (linewise)", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "j", "V", "d", "P")
		assertText(t, m, "line1\nline2\nline3")
	})

	t.Run("multi-line Vd then p restores block below cursor line", func(t *testing.T) {
		m := testEditor("a\nb\nc\nd")
		sendKeys(m, "V", "j", "d", "p")
		assertText(t, m, "c\na\nb\nd")
	})

	t.Run("undo restores deleted lines", func(t *testing.T) {
		m := testEditor("line1\nline2\nline3")
		sendKeys(m, "j", "V", "j", "d")
		assertText(t, m, "line1")
		sendKeys(m, "u")
		assertText(t, m, "line1\nline2\nline3")
	})
}
