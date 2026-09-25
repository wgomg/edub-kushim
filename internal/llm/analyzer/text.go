package analyzer

import (
	"math"
	"strings"
	"unicode"
)

func estimateTokensFromWords(wordCount int) int {
	return int(math.Round(float64(wordCount) * 1.3))
}

func estimateTokens(text string) int {
	var total, cjk int
	for _, r := range text {
		total++
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hangul, r) {
			cjk++
		}
	}
	if total == 0 {
		return 0
	}
	if float64(cjk)/float64(total) > 0.10 {
		return int(math.Ceil(float64(cjk)*1.5 + float64(total-cjk)/4.0))
	}
	return int(math.Ceil(float64(total) / 4.0))
}

func cleanCodeBlock(s string) string {
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```")

	s = strings.TrimSuffix(s, "```")

	return strings.TrimSpace(s)
}

// ContainsNonLatin reports whether the string carries script beyond the
// Latin alphabet (Han, Cyrillic, Arabic, and the rest of the CJK and
// Brahmic families the enricher distinguishes).
func ContainsNonLatin(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Hangul, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Cyrillic, r) ||
			unicode.Is(unicode.Arabic, r) ||
			unicode.Is(unicode.Hebrew, r) ||
			unicode.Is(unicode.Greek, r) ||
			unicode.Is(unicode.Thai, r) ||
			unicode.Is(unicode.Devanagari, r) ||
			unicode.Is(unicode.Bengali, r) ||
			unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
