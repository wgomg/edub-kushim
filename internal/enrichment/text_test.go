package enrichment

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
			got := truncate(tt.input, tt.maxLength)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLength, got, tt.want)
			}
		})
	}
}
