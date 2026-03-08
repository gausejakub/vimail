package logging

import (
	"os"
	"path/filepath"
	"time"
)

const (
	maxLogSize = 10 * 1024 * 1024 // 10 MB
	maxLogAge  = 3 * 24 * time.Hour
)

// rotateIfNeeded checks the current log file and rotates or deletes as needed.
// Called once at Init() before opening the file.
func rotateIfNeeded(logDir string) {
	path := filepath.Join(logDir, "vimail.log")
	info, err := os.Stat(path)
	if err != nil {
		return // no file to rotate
	}

	// Delete if older than 3 days.
	if time.Since(info.ModTime()) > maxLogAge {
		os.Remove(path)
		old := filepath.Join(logDir, "vimail.log.1")
		os.Remove(old)
		return
	}

	// Rotate if over 10MB.
	if info.Size() > maxLogSize {
		old := filepath.Join(logDir, "vimail.log.1")
		os.Remove(old)
		os.Rename(path, old)
	}
}
