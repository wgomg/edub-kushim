package analyzer

import "testing"

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
			got := estimateTokens(tt.text)
			if got != tt.want {
				t.Errorf("estimateTokens(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}
