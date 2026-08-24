package utils

import (
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

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

func EstimateTokens(text string) int {
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

func CleanUp(text string) string {
	re := regexp.MustCompile(`[$€£¥¢%&*+=<>^|~@#\\_\[\]{}]`)
	return re.ReplaceAllString(text, "")
}

func Truncate(s string, maxLength int) string {
	defaultString := "Unknown"

	if strings.ReplaceAll(s, " ", "") == "" {
		return defaultString
	}

	runes := []rune(s)
	if len(runes) <= maxLength {
		return s
	}

	trunc := string(runes[:maxLength])
	trunc = strings.TrimRight(trunc, " \t\n\r")
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

var (
	nonAlphaSpace = regexp.MustCompile(`[^a-z ]`)
	multiSpace    = regexp.MustCompile(` +`)
)

// FoldAccents decomposes, drops combining marks (Mn), and recomposes.
// Stateless per-call: the previous shared transform.Chain singleton
// (golang.org/x/text/transform) raced under concurrent use.
func FoldAccents(s string) string {
	decomp := norm.NFD.String(s)
	out := make([]rune, 0, utf8.RuneCountInString(decomp))
	for _, r := range decomp {
		if !unicode.Is(unicode.Mn, r) {
			out = append(out, r)
		}
	}
	return norm.NFC.String(string(out))
}

func NormalizeForDB(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "\u2013", " ") // en dash
	s = strings.ReplaceAll(s, "\u2014", " ") // em dash
	s = strings.ReplaceAll(s, "\u2010", " ") // hyphen
	s = strings.ReplaceAll(s, "\u2011", " ") // non-breaking hyphen
	s = strings.ReplaceAll(s, "\uFF0D", " ") // fullwidth hyphen-minus

	s = FoldAccents(s)

	s = nonAlphaSpace.ReplaceAllString(s, "")
	s = multiSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	if s == "" {
		return ""
	}
	return s
}

func StripTags(input string) string {
	var result []byte
	for i := 0; i < len(input); i++ {
		if input[i] == '<' {
			j := strings.IndexByte(input[i+1:], '>')
			if j >= 0 {
				i += j + 1
				continue
			}
		}
		result = append(result, input[i])
	}
	return string(result)
}

func StripTagsPtr(input *string) *string {
	if input == nil {
		return nil
	}
	s := StripTags(*input)
	return &s
}
