package email

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// markdown renders message text written in Markdown. Raw HTML in the text
// is not passed through (it is escaped), and links with script schemes are
// dropped: the renderer's safe defaults are kept on purpose, because the
// text may contain a quoted original written by someone else.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	// A line break in a mail body is a line break.
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// MarkdownToHTML renders a message body written in Markdown as an HTML
// document for the text/html alternative of an outgoing message.
func MarkdownToHTML(body string) (string, error) {
	var out bytes.Buffer
	if err := markdown.Convert([]byte(strings.ReplaceAll(body, "\r\n", "\n")), &out); err != nil {
		return "", err
	}
	return "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"></head><body>\n" + out.String() + "</body></html>\n", nil
}
