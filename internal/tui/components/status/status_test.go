package status

import (
	"strings"
	"testing"
	"time"

	"github.com/gausejakub/vimail/internal/tui/util"
)

func TestSpinnerFrameIsTimeDerived(t *testing.T) {
	a := SpinnerFrame(time.UnixMilli(0))
	b := SpinnerFrame(time.UnixMilli(100))
	if a == b {
		t.Fatalf("consecutive frames both %q", a)
	}
}

func TestStatusTTLAndAdaptiveTick(t *testing.T) {
	m := New()
	if !m.IsFullyIdle() || m.TickInterval(time.Now()) != 0 {
		t.Fatal("fresh status should be fully idle with no tick")
	}
	updated, cmd := m.Update(util.InfoMsg{Text: "saved"})
	if cmd == nil {
		t.Fatal("info did not arm tick")
	}
	if updated.infoExpires.IsZero() {
		t.Fatal("info expiry missing")
	}
	updated, _ = updated.Update(util.MotionTickMsg{Now: updated.infoExpires.Add(time.Millisecond)})
	if updated.infoText != "" {
		t.Fatalf("expired info=%q", updated.infoText)
	}
	updated, cmd = updated.Update(util.ProcessStartMsg{ID: "sync", Label: "Syncing"})
	if cmd == nil {
		t.Fatal("active process did not arm animation")
	}
	if updated.IsFullyIdle() || updated.TickInterval(time.Now()) != 100*time.Millisecond {
		t.Fatal("active process should use the 100ms ladder")
	}
}

func TestErrorPrefixAndProgressBar(t *testing.T) {
	m, _ := New().Update(util.InfoMsg{Text: "failed", IsError: true})
	if !strings.HasPrefix(m.infoText, "ERR:") {
		t.Fatalf("error text=%q", m.infoText)
	}
	bar := ProgressBar(1, 2, 5)
	if !strings.Contains(bar, "╸") && len([]rune(bar)) != 5 {
		t.Fatalf("bar=%q", bar)
	}
}

func TestMotionDisabledRendersStaticProcessWithoutAnimationTick(t *testing.T) {
	m := New().SetMotion(false).SetWidth(100)
	updated, cmd := m.Update(util.ProcessStartMsg{ID: "sync", Label: "Syncing"})
	if cmd != nil {
		t.Fatal("motion=false scheduled an animation tick")
	}
	if !strings.Contains(updated.View(), "◌") {
		t.Fatalf("static process view=%q", updated.View())
	}
}
