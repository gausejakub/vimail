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
			}
			stripActiveNodes(c)
		}
		c = next
	}
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
