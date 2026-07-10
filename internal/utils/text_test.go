package utils

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLength int
		want      string
	}{
		{"blank input", "", 10, "Unknown"},
		{"spaces only", "   ", 10, "Unknown"},
		{"short ascii", "hello", 10, "hello"},
		{"exactly maxLength", "abcdefghij", 10, "abcdefghij"},
		{"over maxLength ascii", "abcdefghijklmnopqrstuvwxyz", 10, "abcdefghij"},
		{"trim trailing space", "Hello World This Is A Test", 12, "Hello World"},
		{"trim trailing newline", "abcde\nfghij\nklmno", 6, "abcde"},
		{"no trim when last char is not whitespace", "abcde fghij klmno", 10, "abcde fghi"},
		{"multi-byte japanese", "日本語テスト文字列です", 5, "日本語テス"},
		{"multi-byte cyrillic", "Кириллица текста длинное", 10, "Кириллица"},
		{"mixed ascii and multi-byte", "日本語abc", 5, "日本語ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxLength)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLength, got, tt.want)
			}
		})
	}
}

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
