package email

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const probeBase = "http://127.0.0.1:48911/"

// hostileHTML are bodies that try to make the browser navigate or call out by
// themselves. Each uses its own path so a listener can tell which one got out.
var hostileHTML = map[string]string{
	"refresh-head":      `<html><head><meta http-equiv="refresh" content="0;url=` + probeBase + `refresh-head"></head><body>x</body></html>`,
	"refresh-body":      `<p>x</p><meta http-equiv="refresh" content="0;url=` + probeBase + `refresh-body">`,
	"refresh-upper":     `<META HTTP-EQUIV=REFRESH CONTENT="0;URL=` + probeBase + `refresh-upper">`,
	"refresh-entity":    `<meta http-equiv="&#114;efresh" content="0;url=` + probeBase + `refresh-entity">`,
	"refresh-noscript":  `<noscript><meta http-equiv="refresh" content="0;url=` + probeBase + `refresh-noscript"></noscript>`,
	"refresh-comment":   `<noscript><!-- </noscript><meta http-equiv="refresh" content="0;url=` + probeBase + `refresh-comment"> --></noscript>`,
	"refresh-svg":       `<svg><meta http-equiv="refresh" content="0;url=` + probeBase + `refresh-svg"></svg>`,
	"refresh-svg-style": `<svg><style><meta http-equiv="refresh" content="0;url=` + probeBase + `refresh-svg-style"></style></svg>`,
	"refresh-math":      `<math><mtext><meta http-equiv="refresh" content="0;url=` + probeBase + `refresh-math"></mtext></math>`,
	"refresh-dup-attr":  `<meta http-equiv="content-type" http-equiv="refresh" content="0;url=` + probeBase + `refresh-dup-attr">`,
	"iframe-srcdoc":     `<iframe srcdoc="<meta http-equiv=refresh content='0;url=` + probeBase + `iframe-srcdoc'>"></iframe>`,
	"iframe-src":        `<iframe src="` + probeBase + `iframe-src"></iframe>`,
	"object":            `<object data="` + probeBase + `object"></object><embed src="` + probeBase + `embed">`,
	"link":              `<link rel="stylesheet" href="` + probeBase + `link-css"><link rel="preload" as="image" href="` + probeBase + `link-preload"><link rel="prefetch" href="` + probeBase + `link-prefetch">`,
	"script":            `<script>location="` + probeBase + `script"</script><img src=x onerror="location='` + probeBase + `onerror'"><body onload="location='` + probeBase + `onload'">`,
	"img":               `<img src="` + probeBase + `img"><img srcset="` + probeBase + `srcset 2x"><input type="image" src="` + probeBase + `input-image"><body background="` + probeBase + `body-bg">`,
	"media":             `<video src="` + probeBase + `video" autoplay></video><audio src="` + probeBase + `audio" autoplay></audio><svg><image href="` + probeBase + `svg-image"/></svg>`,
	"css":               `<style>@import url(` + probeBase + `css-import); body{background:url(` + probeBase + `css-bg)}</style><div style="background:url(` + probeBase + `css-inline)">x</div>`,
	"base":              `<base href="` + probeBase + `"><img src="base-img">`,
	"css-escape":        `<style>body{background:u\72l(` + probeBase + `css-escape)}</style><div style="background:\75rl(` + probeBase + `css-escape-attr)">x</div>`,
	"css-quoted":        `<style>@IMPORT "` + probeBase + `css-import-string"; div{background:URL( '` + probeBase + `css-quoted' )}</style><p style="background-image:image-set('` + probeBase + `css-image-set' 1x)">x</p>`,
	"css-font":          `<style>@font-face{font-family:x;src:url(` + probeBase + `css-font)} p{font-family:x} li{list-style-image:url("` + probeBase + `css-list")} a{cursor:url(` + probeBase + `css-cursor),auto}</style><p>x</p><ul><li>y</li></ul>`,
	"css-unclosed":      `<div style="background:url(` + probeBase + `css-unclosed">x</div>`,
	"picture":           `<picture><source srcset="` + probeBase + `picture-source"><img src="` + probeBase + `picture-img"></picture>`,
	"svg-use":           `<svg><use href="` + probeBase + `svg-use#a"/><image xlink:href="` + probeBase + `svg-xlink"/><feImage href="` + probeBase + `svg-feimage"/></svg>`,
	"svg-animate":       `<svg><image href="data:,x"><set attributeName="href" to="` + probeBase + `svg-set"/><animate attributeName="href" values="` + probeBase + `svg-animate"/></image></svg>`,
	"form":              `<form action="` + probeBase + `form"><input name="q"><button formaction="` + probeBase + `formaction">go</button></form>`,
	"table-bg":          `<table background="` + probeBase + `table-bg"><tr><td background="` + probeBase + `td-bg">x</td></tr></table><a href="https://example.com" ping="` + probeBase + `ping">l</a>`,
}

func TestBrowserSafeHTMLRemovesNavigatingElements(t *testing.T) {
	for name, in := range hostileHTML {
		out, err := BrowserSafeHTML(in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		lower := strings.ToLower(out)
		if !strings.HasPrefix(lower, `<html><head><meta charset="utf-8"/><meta http-equiv="content-security-policy"`) {
			t.Errorf("%s: charset and policy are not first in head: %.120s", name, out)
		}
		if n := strings.Count(lower, "http-equiv"); n != 1 && !(name == "refresh-dup-attr" && n == 2) {
			t.Errorf("%s: %d http-equiv metas survive: %s", name, n, out)
		}
		for _, tag := range []string{"<script", "<noscript", "<iframe", "<object", "<embed", "<link", "<base", "<!--", `http-equiv="refresh"`, `http-equiv=refresh`} {
			if strings.Contains(lower, tag) {
				t.Errorf("%s: output still contains %q: %s", name, tag, out)
			}
		}
	}
}

// No rebuilt body may keep a reference to the remote host outside a link the
// reader has to click.
func TestBrowserSafeHTMLRemovesRemoteResourceAttributes(t *testing.T) {
	for name, in := range hostileHTML {
		if strings.HasPrefix(name, "refresh") {
			continue
		}
		out, err := BrowserSafeHTML(in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "127.0.0.1") {
			t.Errorf("%s: remote reference survives: %s", name, out)
		}
	}
	out, _ := BrowserSafeHTML(`<a href="javascript:alert(1)">x</a><a href="HTTPS://example.com/a">y</a><img src="data:image/png;base64,AA==" onclick="x()">`)
	if strings.Contains(out, "javascript") || strings.Contains(out, "onclick") || !strings.Contains(out, `href="HTTPS://example.com/a"`) || !strings.Contains(out, "data:image/png") {
		t.Errorf("link/attribute filtering wrong: %s", out)
	}
}

func TestSafeCSSKeepsLocalStylesAndEmbeddedImages(t *testing.T) {
	in := `p{color:red;background:url("data:image/png;base64,AA==") no-repeat} .a{background:URL(cid:logo)}`
	if out, ok := safeCSS(in); !ok || out != in {
		t.Fatalf("safeCSS changed harmless CSS: ok=%v %q", ok, out)
	}
	out, ok := safeCSS(`a{background:url(https://t.example/p.gif)} @import url(x.css); i{background:image-set(url(https://t.example/q.png) 1x, "r.png" 2x)} b{color:blue}`)
	if !ok || strings.Contains(out, "t.example") || strings.Contains(out, "r.png") || strings.Contains(out, "x.css") || !strings.Contains(out, "b{color:blue}") {
		t.Fatalf("safeCSS = %q ok=%v", out, ok)
	}
	if _, ok := safeCSS(`a{background:u\72l(https://t.example/p.gif)}`); ok {
		t.Fatal("CSS with escapes must be dropped")
	}
}

func TestBrowserSafeHTMLKeepsContent(t *testing.T) {
	out, err := BrowserSafeHTML(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8"><title>Hi</title><style>p{color:red}</style></head><body><p>Hello <a href="https://example.com">link</a></p><img src="cid:logo"></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<title>Hi</title>", "p{color:red}", `<a href="https://example.com">link</a>`, `src="cid:logo"`, `<meta charset="utf-8"/>`, "form-action &#39;none&#39;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	// The message's own charset declaration is replaced by vimail's.
	if strings.Contains(out, "Content-Type") {
		t.Errorf("the message's Content-Type declaration survives in %s", out)
	}
}

func TestBrowserSafeHTMLRejectsUnparseable(t *testing.T) {
	if _, err := BrowserSafeHTML(strings.Repeat("<div>", 2000)); err == nil {
		t.Fatal("expected an error for markup nested beyond the parser limit")
	}
}

// TestWriteBrowserProbes writes the rebuilt hostile bodies to
// $VIMAIL_PROBE_DIR so they can be opened in a real browser against a local
// listener (see the audit notes in the PR).
func TestWriteBrowserProbes(t *testing.T) {
	dir := os.Getenv("VIMAIL_PROBE_DIR")
	if dir == "" {
		t.Skip("VIMAIL_PROBE_DIR not set")
	}
	for name, in := range hostileHTML {
		out, err := BrowserSafeHTML(in)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(out), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// The sender's charset declaration must not survive: a browser told to read
// the file as ISO-2022-JP decodes escape sequences into a two-byte mode that
// swallows quotes and tags (the policy included), so text that was an
// attribute value to the sanitizer becomes a live event handler.
func TestBrowserSafeHTMLForcesUTF8(t *testing.T) {
	in := "<html lang=\"\x1b$B\"><head><meta charset=\"iso-2022-jp\">" +
		"<meta http-equiv=\"Content-Type\" content=\"text/html; charset=iso-2022-jp\"><title>\x1b(B</title></head>" +
		"<body><img alt=\"\x1b$B\">\x1b(B<a title=\" onerror=alert(1) src=x \">x</a>" +
		"<img alt=\"a\" \x1b(Bx=\"y\"></body></html>"
	out, err := BrowserSafeHTML(in)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "iso-2022") || strings.Contains(lower, "content-type") {
		t.Fatalf("the sender's charset declaration survived:\n%s", out)
	}
	if strings.ContainsAny(out, "\x1b\x00\x7f") {
		t.Fatalf("control bytes survived:\n%q", out)
	}
	charset := strings.Index(out, `<meta charset="utf-8"/>`)
	csp := strings.Index(out, "Content-Security-Policy")
	if charset < 0 || csp < 0 || charset > csp {
		t.Fatalf("UTF-8 must be declared before the policy (charset=%d csp=%d):\n%s", charset, csp, out)
	}
	if !strings.HasPrefix(out, `<html lang="$B"><head><meta charset="utf-8"/>`) {
		t.Fatalf("something precedes the charset declaration:\n%s", out)
	}
	if strings.Contains(out, `x="y"`) {
		t.Fatalf("an attribute with an escape sequence in its name survived:\n%q", out)
	}
}

// Removing an @import must not join the text around it into something new:
// "</sty" + "le>" closed the element, and what followed was written out as
// markup no check had seen (a meta refresh, a script).
func TestBrowserSafeHTMLStyleCannotCloseItself(t *testing.T) {
	for _, in := range []string{
		`<style></sty@import x;le><meta http-equiv="refresh" content="0;url=http://evil.example/"></style>hi`,
		`<style></sty@import x;le><script>alert(1)</script><img src="http://evil.example/p.gif"></style>hi`,
		`<style>a{}</STY@import x;LE ><meta http-equiv=refresh content=0></style>`,
	} {
		out, err := BrowserSafeHTML(in)
		if err != nil {
			continue // shown as plain text instead
		}
		lower := strings.ToLower(out)
		if strings.Contains(lower, "refresh") || strings.Contains(lower, "<script") || strings.Contains(lower, "evil.example") {
			t.Errorf("markup escaped from <style>:\n in: %s\nout: %s", in, out)
		}
	}
}

func TestSafeCSSRemovalDoesNotJoinTokens(t *testing.T) {
	for _, in := range []string{
		"a{background:ur@import x;l(http://evil.example/a)}",
		"@namespace x url(\"data:a\n); @import 'http://evil.example/x.css'; y{content:\"z)\"}",
		"a{background:url('data:a' ) } @import \"http://evil.example/x.css\"; b{background:url(http://evil.example/b)}",
	} {
		out, ok := safeCSS(in)
		if !ok {
			continue
		}
		lower := strings.ToLower(out)
		if strings.Contains(lower, "url(http") || strings.Contains(lower, "@import") {
			t.Errorf("remote load survived:\n in: %q\nout: %q", in, out)
		}
	}
}
