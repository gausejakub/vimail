package vimtea

import "unicode/utf8"

// Lines are UTF-8 strings and Cursor.Col is a byte offset into them. A
// character may span several bytes, so every position the editor keeps or
// slices at must sit on a rune boundary. These helpers are the only place
// that knows how to find one.

// runeStart returns the largest rune boundary that is <= col (clamped to the
// line).
func runeStart(line string, col int) int {
	if col >= len(line) {
		return len(line)
	}
	if col < 0 {
		return 0
	}
	// A character is at most four bytes. Look back that far for a start byte
	// whose character really covers col; a stray continuation byte (invalid
	// UTF-8) is its own one-byte character, so col is then a boundary.
	for back := 0; back < utf8.UTFMax && col-back >= 0; back++ {
		s := col - back
		if utf8.RuneStart(line[s]) {
			if _, size := utf8.DecodeRuneInString(line[s:]); s+size > col {
				return s
			}
			break
		}
	}
	return col
}

// runeEnd returns the boundary just past the character that contains byte
// col; for a col at or past the end it returns len(line).
func runeEnd(line string, col int) int {
	if col >= len(line) {
		return len(line)
	}
	start := runeStart(line, col)
	_, size := utf8.DecodeRuneInString(line[start:])
	// Always past col, so loops that step with runeEnd terminate.
	return min(max(start+size, col+1), len(line))
}

// runeCeil returns col if it is a boundary, otherwise the next boundary.
func runeCeil(line string, col int) int {
	if col >= len(line) {
		return len(line)
	}
	if col <= 0 {
		return 0
	}
	if runeStart(line, col) == col {
		return col
	}
	return runeEnd(line, col)
}

// prevRuneStart returns the start of the character before col (0 when there
// is none).
func prevRuneStart(line string, col int) int {
	col = runeStart(line, col)
	if col == 0 {
		return 0
	}
	return runeStart(line, col-1)
}

// lastRuneStart returns the start of the line's last character (0 for an
// empty line): where the cursor rests at the end of a line in normal mode.
func lastRuneStart(line string) int {
	if line == "" {
		return 0
	}
	return runeStart(line, len(line)-1)
}

// advanceRunes returns the boundary n characters after col (stopping at the
// end of the line).
func advanceRunes(line string, col, n int) int {
	col = runeStart(line, col)
	for ; n > 0 && col < len(line); n-- {
		col = runeEnd(line, col)
	}
	return col
}

// findRuneForward returns the byte offset of the first ch at or after from,
// or -1.
func findRuneForward(line string, from int, ch rune) int {
	for i := runeCeil(line, from); i < len(line); i = runeEnd(line, i) {
		if r, _ := utf8.DecodeRuneInString(line[i:]); r == ch {
			return i
		}
	}
	return -1
}

// findRuneBackward returns the byte offset of the last ch that starts before
// col, or -1.
func findRuneBackward(line string, col int, ch rune) int {
	for i := runeStart(line, col); i > 0; {
		i = prevRuneStart(line, i)
		if r, _ := utf8.DecodeRuneInString(line[i:]); r == ch {
			return i
		}
	}
	return -1
}
