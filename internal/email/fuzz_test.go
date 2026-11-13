package email

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func FuzzSanitize(f *testing.F) {
	for _, s := range []string{"plain", "a\x1b[2Jb", "a\x1b]52;c;QUJD\x07b", "x\u202ey", "tab\tnl\n", "\xff\xfe", "\U000E0041", "a\u009b31mb", "\x1b", "\x1b[", "\x1b]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, out := range []string{SanitizeLine(s), SanitizeText(s)} {
			for _, r := range out {
				if r == '\n' || r == '\t' {
					continue
				}
				if isUnsafeRune(r) {
					t.Fatalf("unsafe rune %U survives in %q (input %q)", r, out, s)
				}
			}
		}
		if line := SanitizeLine(s); strings.ContainsAny(line, "\n\r\t") {
			t.Fatalf("SanitizeLine kept a line break or tab: %q", line)
		}
		// Sanitizing is stable: a second pass changes nothing.
		if once := SanitizeText(s); SanitizeText(once) != once {
			t.Fatalf("SanitizeText not idempotent for %q", s)
		}
	})
}

// FuzzBrowserSafeHTML checks the invariant the browser view depends on: the
// rebuilt document, parsed again the way a browser would (with and without
// scripting), contains no element or attribute that navigates or fetches.
func FuzzBrowserSafeHTML(f *testing.F) {
	for _, s := range hostileHTML {
		f.Add(s)
	}
	f.Add(`<svg><style><!--</style><meta http-equiv=refresh content=0>`)
	f.Add(`<noscript><p title="</noscript><meta http-equiv=refresh content=0>">`)
	f.Add(`<textarea><meta http-equiv=refresh></textarea><title><base href=x></title>`)
	f.Add(`<template><meta http-equiv=refresh></template><xmp><script>x</script></xmp>`)
	f.Add(`<math><annotation-xml encoding="text/html"><meta http-equiv=refresh></annotation-xml></math>`)
	f.Add(`<p style="background:url(http://x/y)" onclick=a srcset=b>`)
	f.Fuzz(func(t *testing.T, s string) {
		out, err := BrowserSafeHTML(s)
		if err != nil {
			return
		}
		for _, scripting := range []bool{false, true} {
			doc, err := html.ParseWithOptions(strings.NewReader(out), html.ParseOptionEnableScripting(scripting))
			if err != nil {
				t.Fatalf("rebuilt document does not parse: %v", err)
			}
			policies := 0
			var walk func(n *html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode {
					isPolicy := false
					if strings.EqualFold(n.Data, "meta") {
						for _, a := range n.Attr {
							if strings.EqualFold(a.Key, "http-equiv") && a.Val == "Content-Security-Policy" {
								isPolicy = true
								policies++
							}
						}
					}
					if !isPolicy && isActiveNode(n) {
						t.Fatalf("active <%s> survives (scripting=%v)\nin:  %q\nout: %q", n.Data, scripting, s, out)
					}
					before := len(n.Attr)
					attrs := append([]html.Attribute(nil), n.Attr...)
					if kept := safeAttributes(n); len(kept) != before {
						t.Fatalf("unsafe attribute on <%s> survives (scripting=%v): %v\nin:  %q\nout: %q", n.Data, scripting, attrs, s, out)
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
			if policies < 1 {
				t.Fatalf("policy missing (scripting=%v) in %q", scripting, out)
			}
		}
	})
}

func FuzzSafeCSS(f *testing.F) {
	for _, s := range []string{"a{color:red}", "a{background:url(http://x)}", `@import "x";`, "url(", "URL( 'x' )", "image-set(url(a) 1x)", `u\72l(x)`, "url(data:,x)", "src('x')", "@import", "url(\"x", "image-set("} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out, ok := safeCSS(s)
		if !ok {
			return
		}
		lower := strings.ToLower(out)
		if strings.Contains(lower, "@import") || strings.Contains(lower, "image-set(") || strings.Contains(lower, "src(") || strings.Contains(out, "\\") {
			t.Fatalf("blocked construct survives: %q -> %q", s, out)
		}
		for i := 0; ; {
			j := strings.Index(lower[i:], "url(")
			if j < 0 {
				break
			}
			i += j + 4
			rest := strings.TrimLeft(lower[i:], " \t\n\r\f\v\"'")
			if !strings.HasPrefix(rest, ")") && !strings.HasPrefix(rest, "data:") && !strings.HasPrefix(rest, "cid:") {
				t.Fatalf("remote url() survives: %q -> %q", s, out)
			}
		}
	})
}
