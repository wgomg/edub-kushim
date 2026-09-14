package utils

import "strings"

var jointConjunctions = map[string]struct{}{
	"and":    {},
	"or":     {},
	"vs":     {},
	"vs.":    {},
	"versus": {},
}

func SplitJointTag(name string) []string {
	tokens := strings.Fields(name)
	if len(tokens) == 0 {
		return nil
	}

	var segments []string
	var current []string
	for _, tok := range tokens {
		if _, ok := jointConjunctions[strings.ToLower(tok)]; ok {
			if len(current) == 0 {
				return nil
			}
			segments = append(segments, strings.Join(current, " "))
			current = nil
			continue
		}
		current = append(current, tok)
	}
	if len(current) == 0 {
		return nil
	}
	segments = append(segments, strings.Join(current, " "))
	if len(segments) < 2 {
		return nil
	}
	return segments
}
