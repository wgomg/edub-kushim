package commands

import "testing"

func TestHighlightSnippet_ConvertsMarkersToANSI(t *testing.T) {
	raw := `AT&T said <b>hello</b> to "the world" and <b>goodbye</b>`

	got := highlightSnippet(raw)

	want := "AT&T said \033[1;33mhello\033[0m to \"the world\" and \033[1;33mgoodbye\033[0m"
	if got != want {
		t.Errorf("highlightSnippet mismatch:\n got:  %q\n want: %q", got, want)
	}
}
