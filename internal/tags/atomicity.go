package tags

import (
	"strings"
	"unicode"
)

const MaxCompoundWords = 3

var functionWords = map[string]struct{}{
	"a": {}, "an": {}, "the": {},
	"and": {}, "or": {}, "but": {}, "nor": {}, "vs": {}, "vs.": {}, "versus": {},
	"about": {}, "above": {}, "across": {}, "after": {}, "against": {}, "along": {},
	"among": {}, "around": {}, "at": {}, "before": {}, "behind": {}, "below": {},
	"beneath": {}, "beside": {}, "between": {}, "beyond": {}, "by": {}, "despite": {},
	"down": {}, "during": {}, "except": {}, "for": {}, "from": {}, "in": {},
	"inside": {}, "into": {}, "near": {}, "of": {}, "off": {}, "on": {}, "onto": {},
	"out": {}, "outside": {}, "over": {}, "past": {}, "per": {}, "since": {},
	"through": {}, "throughout": {}, "till": {}, "to": {}, "toward": {},
	"towards": {}, "under": {}, "underneath": {}, "until": {}, "up": {}, "upon": {},
	"via": {}, "with": {}, "within": {}, "without": {},
}

func DeriveParts(name string) []string {
	tokens := strings.Fields(name)
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := functionWords[strings.ToLower(token)]; ok {
			continue
		}
		parts = append(parts, token)
	}
	return parts
}

type Shape string

const (
	ShapeSingleWord Shape = "single_word"
	ShapeCompound   Shape = "compound"
	ShapeTooLong    Shape = "too_long"
	ShapeNonEnglish Shape = "non_english"
	ShapeNoParts    Shape = "no_parts"
)

func ClassifyShape(name string) Shape {
	if !isLatinScript(name) {
		return ShapeNonEnglish
	}
	tokens := strings.Fields(name)
	if len(tokens) == 0 {
		return ShapeNoParts
	}
	if len(tokens) == 1 {
		return ShapeSingleWord
	}
	if len(tokens) > MaxCompoundWords {
		return ShapeTooLong
	}
	if len(DeriveParts(name)) == 0 {
		return ShapeNoParts
	}
	return ShapeCompound
}

func isLatinScript(name string) bool {
	for _, r := range name {
		if unicode.IsLetter(r) && !unicode.In(r, unicode.Latin) {
			return false
		}
	}
	return true
}
