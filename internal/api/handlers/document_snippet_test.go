package handlers

import "testing"

func TestSanitizeSnippetHTML_EscapesInjectedHTML(t *testing.T) {
	raw := "<b>match</b> surrounded by <script>alert(document.cookie)</script> " +
		`and "quotes" & ampersands`

	got := sanitizeSnippetHTML(raw)

	want := "<b>match</b> surrounded by &lt;script&gt;alert(document.cookie)&lt;/script&gt; " +
		"and &#34;quotes&#34; &amp; ampersands"
	if got != want {
		t.Errorf("sanitizeSnippetHTML mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestSanitizeSnippetHTML_PreservesMultipleHighlights(t *testing.T) {
	got := sanitizeSnippetHTML("<b>foo</b> and <b>bar</b>")
	want := "<b>foo</b> and <b>bar</b>"
	if got != want {
		t.Errorf("sanitizeSnippetHTML mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestSanitizeSnippetHTML_PlainTextUnaffected(t *testing.T) {
	got := sanitizeSnippetHTML("no highlights here")
	want := "no highlights here"
	if got != want {
		t.Errorf("sanitizeSnippetHTML mismatch:\n got:  %q\n want: %q", got, want)
	}
}
