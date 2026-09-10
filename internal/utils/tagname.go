package utils

import (
	"regexp"
	"strings"
)

var tagSpaceRE = regexp.MustCompile(` +`)

func NormalizeTagEmbedding(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = tagSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
