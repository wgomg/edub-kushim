package textnorm

import "strings"

var jointConjunctions = map[string]struct{}{
	"and":    {},
	"or":     {},
	"vs":     {},
	"vs.":    {},
	"versus": {},
}

// SplitJointTag splits a compound tag on its coordinating conjunctions
// ("and", "or", "vs", "vs.", "versus", case-insensitive), returning the two
// or more segments joined as they were. A name with no conjunction, with a
// conjunction at either end, or with fewer than two segments is not a joint
// tag and yields nil.
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
