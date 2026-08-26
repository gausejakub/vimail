package logging

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Rotation limits. Variables (not constants) so tests can shrink them to
// deterministic values; production code never mutates them.
var (
	maxLogSize             = int64(10 * 1024 * 1024) // 10 MB
	maxLogAge              = 3 * 24 * time.Hour
	retentionCheckInterval = time.Hour
)

func logPath(logDir string) string     { return filepath.Join(logDir, "vimail.log") }
func rotatedPath(logDir string) string { return filepath.Join(logDir, "vimail.log.1") }

// rotateIfNeeded checks the current log file and rotates or deletes as needed.
// Called once at Init() before opening the file. Live sessions repeat the same
// policy via the drain goroutine's retention ticker.
func rotateIfNeeded(logDir string) {
	pruneRotated(logDir)

	path := logPath(logDir)
	info, err := os.Stat(path)
	if err != nil {
		return // no file to rotate
	}

	// ModTime is when the newest entry was written: if even that is past the
	// retention window, every entry in the file is expired.
	if time.Since(info.ModTime()) > maxLogAge {
		os.Remove(path)
		os.Remove(rotatedPath(logDir))
		return
	}

	if info.Size() > maxLogSize {
		old := rotatedPath(logDir)
		os.Remove(old)
		os.Rename(path, old)
	}
}

// pruneRotated deletes the rotated file once its newest entry (its ModTime —
// the file is frozen after rotation) is older than the retention window.
func pruneRotated(logDir string) {
	old := rotatedPath(logDir)
	if info, err := os.Stat(old); err == nil && time.Since(info.ModTime()) > maxLogAge {
		os.Remove(old)
	}
}

// oldestEntryTime reports when the file's first entry was written, so a
// long-running session can rotate entries out by age. ModTime is useless for
// the active file (every write refreshes it), so parse the first JSON line
// and fall back to ModTime only if that fails.
func oldestEntryTime(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
				return t
			}
		}
	}
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}
