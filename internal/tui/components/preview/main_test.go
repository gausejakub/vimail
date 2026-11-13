package preview

import (
	"os"
	"sync/atomic"
	"testing"
)

// openedExternally counts what tests tried to open; nothing is ever launched.
var openedExternally atomic.Int64

func TestMain(m *testing.M) {
	OpenExternal = func(string) error {
		openedExternally.Add(1)
		return nil
	}
	code := m.Run()
	CleanupTemp()
	os.Exit(code)
}
