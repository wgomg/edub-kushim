package utils

import "testing"

func TestNormalizeTagEmbedding(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTagEmbedding(tt.in); got != tt.want {
				t.Errorf("NormalizeTagEmbedding(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
