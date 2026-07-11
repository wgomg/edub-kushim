package tagmatch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMaxMatchBodyBytes(t *testing.T) {
	tests := []struct {
		name             string
		reduceTargetWords int
		want             int
	}{
		{"negative returns ceiling", -1, maxBodyBytes},
		{"zero disables reduction, returns ceiling", 0, maxBodyBytes},
		{"small value clamps to floor", 10, minBodyBytes},
		{"default 4000 words clamps to floor", 4000, minBodyBytes},
		{"just above floor", 10923, 10923 * bytesPerWord},
		{"just below ceiling", 174762, 174762 * bytesPerWord},
		{"large value clamps to ceiling", 200000, maxBodyBytes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaxMatchBodyBytes(tt.reduceTargetWords)
			if got != tt.want {
				t.Errorf("MaxMatchBodyBytes(%d) = %d, want %d", tt.reduceTargetWords, got, tt.want)
			}
		})
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"ascii under limit", "hello", 10, "hello"},
		{"ascii exactly at limit", "hello", 5, "hello"},
		{"ascii truncates cleanly", "hello world", 5, "hello"},
		{"cjk backs off to rune boundary", "你好世界", 5, "你"},
		{"cjk backs off one full rune", "你好世界", 6, "你好"},
		{"mixed ascii and cjk", "hi你好world", 7, "hi你"},
		{"mixed ascii and cjk at boundary", "hi你好world", 8, "hi你好"},
		{"empty string", "", 10, ""},
		{"zero max bytes", "hello", 0, ""},
		{"single rune ascii", "a", 1, "a"},
		{"single rune cjk exceeds limit", "好", 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.input, tt.maxBytes)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.input, tt.maxBytes, got, tt.want)
			}
			if len(got) > tt.maxBytes {
				t.Errorf("result length %d exceeds maxBytes %d", len(got), tt.maxBytes)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result %q is not valid UTF-8", got)
			}
		})
	}
}

func TestTruncateUTF8Idempotent(t *testing.T) {
	input := strings.Repeat("hello ", 1000)
	truncated := truncateUTF8(input, 100)
	if truncated != truncateUTF8(truncated, 100) {
		t.Error("truncateUTF8 is not idempotent — truncating the result again should yield the same string")
	}
}
