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
		if !strings.HasPrefix(lower, `<html><head><meta http-equiv="content-security-policy"`) {
			t.Errorf("%s: policy is not first in head: %.120s", name, out)
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
// reader has to click or CSS (which the policy blocks).
func TestBrowserSafeHTMLRemovesRemoteResourceAttributes(t *testing.T) {
	for name, in := range hostileHTML {
		if name == "css" || strings.HasPrefix(name, "refresh") {
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

func TestBrowserSafeHTMLKeepsContent(t *testing.T) {
	out, err := BrowserSafeHTML(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8"><title>Hi</title><style>p{color:red}</style></head><body><p>Hello <a href="https://example.com">link</a></p><img src="cid:logo"></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<title>Hi</title>", "p{color:red}", `<a href="https://example.com">link</a>`, `src="cid:logo"`, `http-equiv="Content-Type"`, "form-action &#39;none&#39;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
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
