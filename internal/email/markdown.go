package email

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdown renders message text written in Markdown. The body can contain
// text written by someone else (a quoted original) or by an AI client, and
// the user edits the Markdown source, never sees the rendering, so the
// rendering must not show anything the source does not:
//
//   - raw HTML is not passed through (goldmark's default omits it);
//   - script-scheme links are dropped (goldmark's default);
//   - images are not rendered: no remote content, no tracking pixel;
//   - a link whose text differs from its address shows the address too.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(visibleLinks{}, 100))),
	// A line break in a mail body is a line break.
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// visibleLinks rewrites images into text and makes link addresses visible.
type visibleLinks struct{}

func (visibleLinks) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var images []*ast.Image
	var links []*ast.Link
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Image:
			images = append(images, n)
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			links = append(links, n)
		}
		return ast.WalkContinue, nil
	})
	for _, img := range images {
		alt := strings.TrimSpace(string(nodeText(img, source)))
		label := "[image"
		if alt != "" {
			label += ": " + alt
		}
		label += "] " + string(img.Destination)
		img.Parent().ReplaceChild(img.Parent(), img, ast.NewString([]byte(label)))
	}
	for _, link := range links {
		dest := string(link.Destination)
		shown := strings.TrimSpace(string(nodeText(link, source)))
		if shown == dest || strings.TrimPrefix(dest, "mailto:") == shown {
			continue
		}
		after := ast.NewString([]byte(" (" + dest + ")"))
		link.Parent().InsertAfter(link.Parent(), link, after)
	}
}

// nodeText collects the text inside a node.
func nodeText(n ast.Node, source []byte) []byte {
	var b bytes.Buffer
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
		case *ast.String:
			b.Write(t.Value)
		default:
			b.Write(nodeText(c, source))
		}
	}
	return b.Bytes()
}

// MarkdownToHTML renders a message body written in Markdown as an HTML
// document for the text/html alternative of an outgoing message.
func MarkdownToHTML(body string) (string, error) {
	var out bytes.Buffer
	if err := markdown.Convert([]byte(strings.ReplaceAll(body, "\r\n", "\n")), &out); err != nil {
		return "", err
	}
	return "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"></head><body>\n" + out.String() + "</body></html>\n", nil
}
