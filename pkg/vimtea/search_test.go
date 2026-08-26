package vimtea

import "testing"

// Regression tests for search wrapping and n/N direction semantics
// (issue #6).

func TestSearchWrapping(t *testing.T) {
	t.Run("forward search wraps to earlier match on same line", func(t *testing.T) {
		m := testEditor("abc def abc")
		sendKeys(m, "$", "/", "a", "b", "c", "enter")
		// No match after col 10; wrapscan finds the one at col 0.
		assertCursor(t, m, 0, 0)
	})

	t.Run("forward search wraps across the document", func(t *testing.T) {
		m := testEditor("target\nmiddle\nend")
		sendKeys(m, "G", "/", "t", "a", "r", "g", "e", "t", "enter")
		assertCursor(t, m, 0, 0)
	})

	t.Run("backward search finds match ending at cursor", func(t *testing.T) {
		m := testEditor("abc def abc")
		// Cursor on the final c (col 10): the match starting at col 8
		// ends exactly at the cursor and must be found.
		sendKeys(m, "$", "?", "a", "b", "c", "enter")
		assertCursor(t, m, 0, 8)
	})

	t.Run("backward search wraps across the document", func(t *testing.T) {
		m := testEditor("start\nmiddle\ntarget")
		sendKeys(m, "?", "t", "a", "r", "g", "e", "t", "enter")
		assertCursor(t, m, 2, 0)
	})

	t.Run("single match wraps back to itself", func(t *testing.T) {
		m := testEditor("only one hit here")
		sendKeys(m, "/", "h", "i", "t", "enter")
		assertCursor(t, m, 0, 9)
		sendKeys(m, "n")
		assertCursor(t, m, 0, 9)
	})
}

func TestSearchRepeatDirection(t *testing.T) {
	t.Run("n repeats forward search forward", func(t *testing.T) {
		m := testEditor("abc abc abc")
		sendKeys(m, "/", "a", "b", "c", "enter")
		assertCursor(t, m, 0, 4)
		sendKeys(m, "n")
		assertCursor(t, m, 0, 8)
		sendKeys(m, "n") // wraps
		assertCursor(t, m, 0, 0)
	})

	t.Run("N reverses a forward search", func(t *testing.T) {
		m := testEditor("abc abc abc")
		sendKeys(m, "/", "a", "b", "c", "enter", "n")
		assertCursor(t, m, 0, 8)
		sendKeys(m, "N")
		assertCursor(t, m, 0, 4)
		sendKeys(m, "N")
		assertCursor(t, m, 0, 0)
	})

	t.Run("n repeats backward search backward", func(t *testing.T) {
		m := testEditor("abc\nabc\nabc")
		sendKeys(m, "G", "?", "a", "b", "c", "enter")
		assertCursor(t, m, 1, 0)
		sendKeys(m, "n")
		assertCursor(t, m, 0, 0)
	})

	t.Run("N reverses a backward search", func(t *testing.T) {
		m := testEditor("abc\nabc\nabc")
		sendKeys(m, "G", "?", "a", "b", "c", "enter")
		assertCursor(t, m, 1, 0)
		sendKeys(m, "N")
		assertCursor(t, m, 2, 0)
	})

	t.Run("repeated matches on multiple lines", func(t *testing.T) {
		m := testEditor("foo x\nbar foo\nfoo end")
		sendKeys(m, "/", "f", "o", "o", "enter")
		assertCursor(t, m, 1, 4)
		sendKeys(m, "n")
		assertCursor(t, m, 2, 0)
		sendKeys(m, "n") // wraps to start
		assertCursor(t, m, 0, 0)
		sendKeys(m, "N") // back the other way, wrapping
		assertCursor(t, m, 2, 0)
	})
}
