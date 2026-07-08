package utils

import "testing"

func TestNormalizeForDB(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"ascii lowercase", "jose garcia", "jose garcia"},
		{"uppercase", "JOSE GARCIA", "jose garcia"},
		{"accent folding", "José García", "jose garcia"},
		{"tilde", "niño", "nino"},
		{"umlaut", "Müller", "muller"},
		{"cedilla", "François", "francois"},
		{"hyphen to space", "maria-jose", "maria jose"},
		{"underscore to space", "maria_jose", "maria jose"},
		{"strip punctuation", "O'Brien", "obrien"},
		{"strip quotes", `"Smith"`, "smith"},
		{"strip brackets", "Lee [Jr]", "lee jr"},
		{"collapse spaces", "  jose   garcia  ", "jose garcia"},
		{"empty string", "", ""},
		{"only spaces", "   ", ""},
		{"only punctuation", "...", ""},
		{"mixed unicode", "Ñoño", "nono"},
		{"greek letters", "αβγ", ""},
		{"numbers stripped", "author123", "author"},
		{"preserve internal spaces", "van der berg", "van der berg"},
		{"em dash to space", "smith\u2014jones", "smith jones"},
		{"single char", "A", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeForDB(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeForDB(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
