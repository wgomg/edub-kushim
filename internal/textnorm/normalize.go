package textnorm

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	nonAlphaSpace = regexp.MustCompile(`[^a-z ]`)
	multiSpace    = regexp.MustCompile(` +`)
)

// canonicalHead applies the head both canonical lookup forms share: NFKC
// normalization, lowercase and trim, the dash and underscore variants
// turned into spaces, and accent folding. Each caller keeps its own tail.
func canonicalHead(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "\u2013", " ") // en dash
	s = strings.ReplaceAll(s, "\u2014", " ") // em dash
	s = strings.ReplaceAll(s, "\u2010", " ") // hyphen
	s = strings.ReplaceAll(s, "\u2011", " ") // non-breaking hyphen
	s = strings.ReplaceAll(s, "\uFF0D", " ") // fullwidth hyphen-minus
	return FoldAccents(s)
}

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

// NormalizeForDB produces the canonical lookup form of a human string such
// as a person name or a document title: NFKC normalization, lowercase,
// hyphen and dash variants turned into spaces, accent folding, then removal
// of everything outside a-z and space, collapsed. Lookups must pass both
// sides through this function to match. Empty input stays empty.
func NormalizeForDB(s string) string {
	s = canonicalHead(s)

	s = nonAlphaSpace.ReplaceAllString(s, "")
	s = multiSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	if s == "" {
		return ""
	}
	return s
}
