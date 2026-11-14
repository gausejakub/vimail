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

// Nothing in the HTML version may be hidden from someone reading the
// Markdown source: no remote images, no link whose address is out of view.
func TestMarkdownShowsWhatTheSourceShows(t *testing.T) {
	body := "On Thu, Evil wrote:\n> Log in at [https://bank.example](https://evil.example/login)\n> ![](https://tracker.example/p.gif)\n\n" +
		"![logo](https://cdn.example/logo.png)\nSee [the doc](https://example.test/doc), <https://example.test/raw> and [https://same.example](https://same.example).\n"
	html, err := MarkdownToHTML(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<img") {
		t.Fatalf("image rendered:\n%s", html)
	}
	for _, want := range []string{
		`https://bank.example</a> (https://evil.example/login)`, // disguised link: real address visible
		`[image] https://tracker.example/p.gif`, `[image: logo] https://cdn.example/logo.png`,
		`the doc</a> (https://example.test/doc)`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
	// A link that already shows its address is not repeated.
	for _, once := range []string{"https://example.test/raw", "https://same.example"} {
		if strings.Count(html, ">"+once+"<") != 1 || strings.Contains(html, "("+once+")") {
			t.Errorf("%s repeated:\n%s", once, html)
		}
	}
}
