// Package textnorm holds the canonical string forms used for identity and
// lookup: the tag-name grammar, compound tag splitting, and the lookup
// normalization shared by the tags domain, search, people names, and titles.
// All functions are stateless and safe for concurrent use.
package textnorm

import (
	"regexp"
	"strings"
	"unicode"
)

var tagDisallowedRE = regexp.MustCompile(`[^+#.\p{L}\p{Nd} ]`)

// NormalizeTag applies the tag-name grammar: NFKC normalization, lowercase,
// hyphen and dash variants turned into spaces, accent folding, removal of
// everything that is not a letter, digit, '+', '#', '.' or space, and space
// collapsing. It is the canonical form under which tags are stored, matched
// and counted. A name without any letter or digit normalizes to "", which
// callers must treat as a rejected candidate.
func NormalizeTag(s string) string {
	s = canonicalHead(s)
	s = tagDisallowedRE.ReplaceAllString(s, "")
	s = multiSpace.ReplaceAllString(s, " ")
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
