package preview

import (
	"github.com/gausejakub/vimail/internal/email"
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

// What "open in browser" writes for the browser.
func TestBrowserDocumentIsRebuiltAndFallsBackToText(t *testing.T) {
	hostile := &email.Message{
		Subject:  "Hi",
		Body:     "plain <b>text</b> body",
		HTMLBody: `<html><head><meta http-equiv="refresh" content="0;url=https://evil.example/"></head><body><img srcset="https://evil.example/p.gif 1x"><script>x()</script><p>Hello</p></body></html>`,
	}
	doc := browserDocument(hostile)
	lower := strings.ToLower(doc)
	if !strings.Contains(lower, "content-security-policy") || !strings.Contains(doc, "<p>Hello</p>") {
		t.Fatalf("rebuilt document lacks the policy or the content: %s", doc)
	}
	for _, bad := range []string{"refresh", "evil.example", "<script"} {
		if strings.Contains(lower, bad) {
			t.Errorf("rebuilt document still contains %q: %s", bad, doc)
		}
	}

	// Markup the parser refuses (nested too deeply) must not reach the browser
	// raw: the escaped text body is shown instead.
	unparseable := &email.Message{Body: "plain <b>text</b> body", HTMLBody: strings.Repeat("<div>", 2000) + `<meta http-equiv="refresh" content="0;url=https://evil.example/">`}
	doc = browserDocument(unparseable)
	if strings.Contains(doc, "evil.example") || !strings.Contains(doc, "plain &lt;b&gt;text&lt;/b&gt; body") {
		t.Fatalf("fallback document wrong: %.300s", doc)
	}

	// Text-only mail: escaped, no markup from the message.
	if doc := browserDocument(&email.Message{Subject: "<script>s</script>", Body: "<img src=x>"}); strings.Contains(doc, "<script>s") || strings.Contains(doc, "<img src=x>") {
		t.Fatalf("text body not escaped: %s", doc)
	}
}

// An HTML-only message whose text conversion is empty takes the regex
// fallback; it used to panic on an uncompilable pattern.
func TestHTMLOnlyMessageWithoutTextRenders(t *testing.T) {
	got := preparedBody("", `<html><head><style>p{color:red}</style><script>x()</script></head><body><p>Hello</p><br><div>World</div></body></html>`)
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") || strings.Contains(got, "color:red") || strings.Contains(got, "x()") {
		t.Fatalf("preparedBody = %q", got)
	}
	if got := preparedBody("", `<img src="https://example.com/banner.png">`); got == "" {
		t.Fatal("image-only message rendered as nothing")
	}
}

func TestExternalOpenerPerPlatform(t *testing.T) {
	path := `C:\Users\me\AppData\Local\Temp\vimail-1\msg & calc.html`
	for goos, want := range map[string]string{"darwin": "open", "linux": "xdg-open", "freebsd": "xdg-open", "windows": "rundll32"} {
		name, args := externalOpener(goos, path)
		if name != want || args[len(args)-1] != path {
			t.Errorf("%s: %s %q", goos, name, args)
		}
	}
}
