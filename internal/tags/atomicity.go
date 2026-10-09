package tags

import (
	"strings"
	"unicode"
)

// MaxCompoundWords is the largest word count a tag name may have and
// still classify as a compound rather than too long.
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

// DeriveParts splits a tag name into its content words, dropping function
// words (articles, conjunctions, prepositions). The parts are what
// reconstructibility and part-coverage readings compare against.
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

// Shape is the atomicity classification of a tag name.
type Shape string

// The shapes a name can take: a single word, a compound of at most
// MaxCompoundWords words, more words than that, non-Latin script, or no
// content parts after function words are dropped.
const (
	ShapeSingleWord Shape = "single_word"
	ShapeCompound   Shape = "compound"
	ShapeTooLong    Shape = "too_long"
	ShapeNonEnglish Shape = "non_english"
	ShapeNoParts    Shape = "no_parts"
)

// ClassifyShape classifies a tag name's atomicity shape. Non-Latin-script
// and empty names are never compounds.
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

// periodUnits is the pinned, closed set of unit nouns the period class accepts; adding a unit is a
// criteria change, not a config.
var periodUnits = map[string]struct{}{"century": {}, "decade": {}, "millennium": {}}

// IsPeriodClass reports whether a name is a time period in the house numeric style: an ordinal token
// (<digits>st|nd|rd|th) followed by century, decade, or millennium. The caller must pass the house form —
// the function does not normalize — so a spelled ordinal reaches the class only after HouseForm
// (nineteenth → 19th). The class is the split gate's deterministic keep (report N1): the gate keeps such a
// candidate whole without judging it and without applying its parts.
func IsPeriodClass(name string) bool {
	tokens := strings.Fields(strings.ToLower(name))
	if len(tokens) != 2 {
		return false
	}
	if _, ok := periodUnits[tokens[1]]; !ok {
		return false
	}
	return isOrdinalToken(tokens[0])
}

func isOrdinalToken(tok string) bool {
	digits, suffix := splitNumericToken(tok)
	return digits > 0 && isOrdinalSuffix(suffix)
}
