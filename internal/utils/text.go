package utils

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func CountWords(text string) int {
	words := strings.Fields(text)
	wordCount := len(words)

	return wordCount
}

func EstimateTokensFromWords(wordCount int) int {
	return int(math.Round(float64(wordCount) * 1.3))
}

func CleanUp(text string) string {
	re := regexp.MustCompile(`[$€£¥¢%&*+=<>^|~@#\\_\[\]{}]`)
	return re.ReplaceAllString(text, "")
}

func Truncate(s string, maxLength int) string {
	defaultString := "Unknown"

	if strings.ReplaceAll(s, " ", "") == "" {
		return defaultString
	}

	if len(s) <= maxLength {
		return s
	}

	trunc := (s)[:maxLength]
	return trunc
}

func CleanCodeBlock(s string) string {
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```")

	s = strings.TrimSuffix(s, "```")

	return strings.TrimSpace(s)
}

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

// NormalizeName normalizes a name for consistent matching.
// Steps: NFKC normalization → lowercase → remove dots, commas, apostrophes,
// quotes → replace dashes with space → collapse whitespace.
func NormalizeName(name string) string {
	name = norm.NFKC.String(name)

	name = strings.ToLower(name)

	name = strings.NewReplacer(
		".", "",
		",", "",
		"'", "",
		"’", "",
		"ʻ", "",
		"\"", "",
		"“", "",
		"”", "",
		"«", "",
		"»", "",
		":", "",
		";", "",
		"!", "",
		"?", "",
		"`", "",
		"_", "",
	).Replace(name)

	name = strings.NewReplacer(
		"-", " ",
		"–", " ",
		"—", " ",
		"－", " ",
		"‐", " ",
		"‑", " ",
	).Replace(name)

	name = strings.Join(strings.Fields(name), " ")
	return strings.TrimSpace(name)
}
