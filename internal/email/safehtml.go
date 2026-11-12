package email

import (
	"bytes"
	"errors"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// browserCSP blocks everything a message's own markup could load or run when
// it is opened in a browser: scripts, remote images and styles, frames,
// media, and form submission.
const browserCSP = "default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'"

// BrowserSafeHTML prepares an untrusted HTML body for viewing in a browser.
//
// A Content-Security-Policy alone is not enough: it does not stop
// <meta http-equiv="refresh">, which sends the tab to a sender-chosen URL as
// soon as the message opens (confirming the open, or landing on a phishing
// page). The body is therefore parsed and rebuilt with the policy first in
// <head> and without the elements that can navigate or call out on their own:
// meta http-equiv (except Content-Type), <base>, <link>, <script>, frames and
// plugins, and <noscript> (whose content is markup only for browsers with
// scripting off, the usual place to hide a refresh). Comments are dropped as well, so the
// rebuilt markup cannot be re-read differently by the browser.
//
// It returns an error for markup that cannot be parsed (e.g. nested too
// deeply); callers should then fall back to the plain-text body.
func BrowserSafeHTML(raw string) (string, error) {
	doc, err := html.ParseWithOptions(strings.NewReader(raw), html.ParseOptionEnableScripting(false))
	if err != nil {
		return "", err
	}
	stripActiveNodes(doc)

	head := findElement(doc, atom.Head)
	if head == nil {
		return "", errors.New("html: document has no head")
	}
	csp := &html.Node{
		Type: html.ElementNode, DataAtom: atom.Meta, Data: "meta",
		Attr: []html.Attribute{
			{Key: "http-equiv", Val: "Content-Security-Policy"},
			{Key: "content", Val: browserCSP},
		},
	}
	head.InsertBefore(csp, head.FirstChild)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func stripActiveNodes(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if isActiveNode(c) {
			n.RemoveChild(c)
		} else {
			if c.Type == html.ElementNode {
				c.Attr = safeAttributes(c)
				if strings.EqualFold(c.Data, "style") && !safeStyleElement(c) {
					n.RemoveChild(c)
					c = next
					continue
				}
			}
			stripActiveNodes(c)
		}
		c = next
	}
}

// safeStyleElement rewrites the CSS inside a <style> element; it reports
// false when the element has to be dropped.
func safeStyleElement(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.TextNode {
			return false
		}
		css, ok := safeCSS(c.Data)
		if !ok {
			return false
		}
		c.Data = css
	}
	return true
}

// safeAttributes drops the attributes that make a browser fetch something or
// run code on its own. The policy should already block those loads, but it is
// not applied uniformly: Firefox fetches <img srcset> and <picture><source>
// despite img-src, which is all a tracking pixel needs. Links the reader has
// to click (<a>, <area>) stay, limited to ordinary schemes.
func safeAttributes(n *html.Node) []html.Attribute {
	tag := strings.ToLower(n.Data)
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		val := strings.ToLower(strings.TrimSpace(a.Val))
		switch {
		case strings.HasPrefix(key, "on"): // event handlers
			continue
		case key == "style":
			css, ok := safeCSS(a.Val)
			if !ok {
				continue
			}
			a.Val = css
		case key == "srcset", key == "imagesrcset", key == "ping", key == "manifest",
			key == "action", key == "formaction":
			continue
		case key == "src", key == "poster", key == "background", key == "data",
			key == "lowsrc", key == "dynsrc":
			if !hasScheme(val, "data:", "cid:") {
				continue
			}
		case key == "href":
			if tag == "a" || tag == "area" {
				if !hasScheme(val, "http:", "https:", "mailto:", "tel:", "#") {
					continue
				}
			} else if !hasScheme(val, "data:", "cid:", "#") {
				continue // <image>, <use>, <feImage>, …
			}
		}
		kept = append(kept, a)
	}
	return kept
}

// safeCSS removes what makes CSS fetch remote resources: url() with anything
// but an embedded data:/cid: target, @import, and the image-set()/src()
// functions (which also accept a bare string). CSS escapes can spell any of
// these so that a text scan misses them (u\72l(…)), so a style containing a
// backslash is dropped entirely; ok is then false.
func safeCSS(css string) (out string, ok bool) {
	if strings.Contains(css, "\\") {
		return "", false
	}
	lower := strings.ToLower(css)
	var b strings.Builder
	for i := 0; i < len(css); {
		switch {
		case strings.HasPrefix(lower[i:], "@import"):
			// Drop the whole rule, up to and including its ";".
			if end := strings.IndexByte(css[i:], ';'); end >= 0 {
				i += end + 1
			} else {
				i = len(css)
			}
		case strings.HasPrefix(lower[i:], "image-set("):
			b.WriteString("x-blocked()")
			i = cssCallEnd(css, i+len("image-set(")) + 1
		case strings.HasPrefix(lower[i:], "src("):
			b.WriteString("x-blocked()")
			i = cssCallEnd(css, i+len("src(")) + 1
		case strings.HasPrefix(lower[i:], "url("):
			end := cssURLEnd(css, i+4)
			target := strings.ToLower(strings.Trim(strings.TrimSpace(css[i+4:end]), `"'`))
			if hasScheme(strings.TrimSpace(target), "data:", "cid:") && end < len(css) {
				b.WriteString(css[i : end+1])
			} else {
				b.WriteString("url()")
			}
			i = end + 1
		default:
			b.WriteByte(css[i])
			i++
		}
	}
	return b.String(), true
}

// cssURLEnd returns the index of the ")" closing a url( that starts at from,
// honouring a quoted argument, or len(css) when it is never closed.
func cssURLEnd(css string, from int) int {
	i := from
	for i < len(css) && (css[i] == ' ' || css[i] == '\t' || css[i] == '\n' || css[i] == '\r') {
		i++
	}
	if i < len(css) && (css[i] == '"' || css[i] == '\'') {
		quote := css[i]
		for i++; i < len(css) && css[i] != quote; i++ {
		}
	}
	for ; i < len(css); i++ {
		if css[i] == ')' {
			return i
		}
	}
	return len(css)
}

// cssCallEnd returns the index of the ")" closing a function call whose
// arguments start at from, honouring nested parentheses and quoted strings,
// or len(css) when it is never closed.
func cssCallEnd(css string, from int) int {
	depth := 1
	for i := from; i < len(css); i++ {
		switch css[i] {
		case '"', '\'':
			quote := css[i]
			for i++; i < len(css) && css[i] != quote; i++ {
			}
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(css)
}

func hasScheme(val string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(val, p) {
			return true
		}
	}
	return false
}

func isActiveNode(n *html.Node) bool {
	switch n.Type {
	case html.CommentNode:
		return true
	case html.ElementNode:
		switch strings.ToLower(n.Data) {
		case "base", "link", "script", "noscript",
			"iframe", "frame", "frameset", "object", "embed", "applet",
			// SVG animation can set href/src on another element after load.
			"animate", "set", "animatetransform", "animatemotion":
			return true
		case "meta":
			for _, a := range n.Attr {
				if strings.EqualFold(a.Key, "http-equiv") && !strings.EqualFold(strings.TrimSpace(a.Val), "content-type") {
					return true
				}
			}
		}
	}
	return false
}

func findElement(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, a); found != nil {
			return found
		}
	}
	return nil
}
