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

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"empty string", "", 0},
		{"pure ascii short", "hello", 2},
		{"pure ascii long", "the quick brown fox jumps over the lazy dog", 11},
		{"ascii with spaces", "a b c d e f g h i j", 5},
		{"cjk heavy japanese", "日本語テスト文字列です", 17},
		{"cjk heavy chinese", "这是一个中文文本测试", 15},
		{"cjk heavy korean", "한국어 텍스트 테스트입니다", 19},
		{"mixed low cjk ratio", "hello world 你好", 6},
		{"mixed high cjk ratio", "hello 日本語テスト", 11},
		{"single char ascii", "a", 1},
		{"single char cjk", "日", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.text)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.text, got, tt.want)
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
