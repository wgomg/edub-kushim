package utils

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var tagSpaceRE = regexp.MustCompile(` +`)
var tagDisallowedRE = regexp.MustCompile(`[^+#.\p{L}\p{Nd} ]`)

func NormalizeTag(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "\u2013", " ")
	s = strings.ReplaceAll(s, "\u2014", " ")
	s = strings.ReplaceAll(s, "\u2010", " ")
	s = strings.ReplaceAll(s, "\u2011", " ")
	s = strings.ReplaceAll(s, "\uFF0D", " ")
	s = FoldAccents(s)
	s = tagDisallowedRE.ReplaceAllString(s, "")
	s = tagSpaceRE.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if !containsLetterOrDigit(s) {
		return ""
	}
	return s
}

func containsLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}