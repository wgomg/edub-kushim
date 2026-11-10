package database

import "testing"

func TestLikeContains(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"empty text yields the match-everything pattern", "", "%%"},
		{"plain text is wrapped in wildcards", "sym", "%sym%"},
		{"LIKE metacharacters are escaped", `50%_a\b`, `%50\%\_a\\b%`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LikeContains(tt.text); got != tt.want {
				t.Errorf("LikeContains(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
