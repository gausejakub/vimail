package email

import (
	"strings"
	"testing"
)

func TestMarkdownToHTML(t *testing.T) {
	body := "Hi **Petr**,\n\nthe plan:\n\n- first\n- second\n\nSee [the doc](https://example.test/doc) or https://example.test/raw.\nSecond line.\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n-- \nJakub\n\nOn Thu, Petr wrote:\n> are you coming?\n> yes?\n"
	html, err := MarkdownToHTML(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<strong>Petr</strong>", "<li>first</li>", `<a href="https://example.test/doc">the doc</a>`,
		`<a href="https://example.test/raw">https://example.test/raw</a>`, // bare links
		"or <a ", "<br>", "<table>", "<blockquote>", "are you coming?", `<meta charset="utf-8">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}

	// The body can quote someone else's text: nothing in it may become
	// active HTML.
	hostile := "<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[click](javascript:alert(1))\n\n" +
		"> <iframe src=\"https://evil.test\"></iframe>\n\n<a href=\"https://evil.test\" onclick=\"x()\">x</a>\n"
	html, err = MarkdownToHTML(hostile)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "<img", "<iframe", "onerror=\"", "onclick=\"", "href=\"javascript:", "<a href=\"https://evil.test\" onclick"} {
		if strings.Contains(strings.ToLower(html), bad) {
			t.Errorf("html contains %q:\n%s", bad, html)
		}
	}
}
