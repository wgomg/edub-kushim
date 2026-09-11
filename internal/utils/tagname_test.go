package utils

import "testing"

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"passthrough", "foo", "foo"},
		{"hyphen to space", "foo-bar", "foo bar"},
		{"underscore to space", "foo_bar", "foo bar"},
		{"collapse runs of spaces", "foo  bar", "foo bar"},
		{"trim whitespace", "  foo  ", "foo"},
		{"mixed", "  foo-bar_baz  qux  ", "foo bar baz qux"},
		{"empty", "", ""},
		{"lowercased", "Anarchism", "anarchism"},
		{"keeps symbols", "C++", "c++"},
		{"keeps digits", "c2", "c2"},
		{"keeps dot", "asp.net", "asp.net"},
		{"folds accents", "wörld cup", "world cup"},
		{"keeps non-latin letters", "история", "история"},
		{"strips apostrophe", "don't", "dont"},
		{"pure symbols to empty", "...", ""},
		{"en dash to space", "foo\u2013bar", "foo bar"},
		{"em dash to space", "foo\u2014bar", "foo bar"},
		{"fullwidth hyphen to space", "foo\uFF0Dbar", "foo bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTag(tt.in); got != tt.want {
				t.Errorf("NormalizeTag(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}