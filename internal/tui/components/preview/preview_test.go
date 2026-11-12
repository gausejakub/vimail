package preview

import (
	"strings"
	"testing"
	"time"
)

// A body of repeated '[' used to restart a JSON decoder at every byte on every
// frame, freezing the UI (and again on every launch, since the body is cached).
func TestHostileBodiesRenderQuickly(t *testing.T) {
	for name, body := range map[string]string{
		"brackets": strings.Repeat("[", 1<<20),
		"braces":   strings.Repeat("{\"a\":", 200_000),
		"huge":     strings.Repeat("lorem ipsum dolor sit amet ", 1<<20),
	} {
		start := time.Now()
		for i := 0; i < 20; i++ { // repeated frames must hit the cache
			wrappedBody(body, "", 80)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: 20 renders took %v", name, d)
		}
	}
}

func TestPrettyJSONStillFormatsEmbeddedObjects(t *testing.T) {
	got := prettyJSON(`event: {"type":"bounce","id":12345}`)
	if !strings.Contains(got, "\n  \"type\": \"bounce\"") {
		t.Fatalf("prettyJSON = %q", got)
	}
}

func TestBodyEscapesNeverReachOutput(t *testing.T) {
	out := wrappedBody("hi\x1b]52;c;QUJD\x07 there\x1b[2J\u009b31m", "", 80)
	if strings.ContainsAny(out, "\x1b\x07\u009b") {
		t.Fatalf("escape survived: %q", out)
	}
}

// Entities are decoded before sanitizing, so they cannot reintroduce what the
// sanitizer removes.
func TestEntityEncodedControlsNeverReachOutput(t *testing.T) {
	out := preparedBody("a&#x2066;b&#x2069; c&#x061C;d e&#xE0041;f g&#27;[2Jh i&#x202E;j", "")
	if out != "ab cd ef gh ij" {
		t.Fatalf("preparedBody = %q", out)
	}
}
