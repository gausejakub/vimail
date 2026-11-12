package worker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/config"
)

func TestFetchBodySupersededBySelectionOrder(t *testing.T) {
	old := fetchDebounce
	fetchDebounce = time.Millisecond
	defer func() { fetchDebounce = old }()

	c := NewCoordinator(config.Config{}, testQueueStore(t))
	// Commands are created in the user's selection order (on the UI loop) …
	first := c.FetchBody("a@example.com", "Inbox", 1)
	_ = c.FetchBody("a@example.com", "Inbox", 2)

	// … so the older one is skipped even if it runs last, without any IMAP work.
	res, ok := first().(FetchBodyResult)
	if !ok {
		t.Fatalf("unexpected msg type %T", res)
	}
	if !errors.Is(res.Err, ErrFetchSuperseded) {
		t.Fatalf("err = %v, want ErrFetchSuperseded", res.Err)
	}
	if got := c.wantedFetchUID("a@example.com"); got != 2 {
		t.Fatalf("wanted = %d, want 2", got)
	}
	if got := c.wantedFetchUID("other@example.com"); got != 0 {
		t.Fatalf("other account wanted = %d, want 0 (per-account)", got)
	}
}

func TestReadLimitedDrainConsumesOversizedBody(t *testing.T) {
	r := strings.NewReader(strings.Repeat("x", 100))
	data, err := readLimitedDrain(r, 10)
	if err != nil || len(data) != 10 {
		t.Fatalf("data=%d err=%v, want 10 bytes", len(data), err)
	}
	if r.Len() != 0 {
		t.Fatalf("%d bytes left unread; the literal must be fully consumed", r.Len())
	}
}
