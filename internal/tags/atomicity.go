package tags

import (
	"slices"
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

// DeriveSegments returns the decomposition fan of a name: its content words plus every contiguous
// bigram over them, in order (singles first, then bigrams). For a two-word compound the fan is
// {w1, w2, w1 w2}; for three content words {w1, w2, w3, w1w2, w2w3}. Callers fan compound-shaped
// names only (docs/specs/tags/atomicity.md §3.1).
func DeriveSegments(name string) []string {
	content := DeriveParts(name)
	segments := make([]string, 0, len(content)*2)
	segments = append(segments, content...)
	for i := 0; i+1 < len(content); i++ {
		segments = append(segments, content[i]+" "+content[i+1])
	}
	return segments
}

// GreedyCover returns the coarsest in-order cover of the content words by spans resolve reports
// usable, choosing the longest span at each position (a contiguous bigram before a single word) and
// the leftmost among equals. resolve maps a raw span to its applied name and reports whether it counts
// as a value; the cover holds the applied names. A span covering the whole sequence is never a cover
// segment — the whole phrase is not a decomposition of itself, so a two-word compound decomposes into
// its two words or keeps whole. With alongside false it returns nil when no set of usable spans tiles
// the whole sequence — the decomposition does not exist and the compound keeps whole. With alongside
// true it skips words no usable span covers and returns the partial cover, the parts a kept compound
// applies alongside itself (docs/specs/tags/atomicity.md §3.1).
func GreedyCover(content []string, resolve func(string) (string, bool), alongside bool) []string {
	cover := make([]string, 0, len(content))
	for i := 0; i < len(content); {
		if i+1 < len(content) && !(i == 0 && i+2 == len(content)) {
			if applied, ok := resolve(content[i] + " " + content[i+1]); ok {
				cover = append(cover, applied)
				i += 2
				continue
			}
		}
		if applied, ok := resolve(content[i]); ok {
			cover = append(cover, applied)
			i++
			continue
		}
		if alongside {
			i++
			continue
		}
		return nil
	}
	return cover
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
// (nineteenth → 19th). The class marks the seeded period shape: the split gate treats a store-existing
// period as an application that keeps whole without judging it and without applying its parts, while an
// unseeded period shape is dropped by the time-designation gate before it reaches the split gate.
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

func isPeriodUnit(tok string) bool {
	_, ok := periodUnits[tok]
	return ok
}

// monthNames is the pinned, closed set of English month nouns the time-designation gate recognizes.
var monthNames = map[string]struct{}{
	"january": {}, "february": {}, "march": {}, "april": {}, "may": {}, "june": {},
	"july": {}, "august": {}, "september": {}, "october": {}, "november": {}, "december": {},
}

// IsTimeDesignation reports whether a name is a bare time designation that is not itself a subject
// dimension: a year (three or four digits), a decade (`\d{2,4}s` or a spelled decade word), a month name,
// a month followed by a year, or an ordinal period unit (`19th century`, `18th century`). It is a shape
// test only; admissibility is store membership — a seeded period is a store value and is never a drop.
func IsTimeDesignation(name string) bool {
	tokens := strings.Fields(strings.ToLower(name))
	switch len(tokens) {
	case 1:
		return isMonthName(tokens[0]) || isSpelledDecade(tokens[0]) || isYearToken(tokens[0]) || isDecadeToken(tokens[0])
	case 2:
		if isMonthName(tokens[0]) && isYearToken(tokens[1]) {
			return true
		}
		return isOrdinalToken(tokens[0]) && isPeriodUnit(tokens[1])
	default:
		return false
	}
}

// IsTimeDesignationDrop reports whether a name is a time designation that must not become a tag: it is a
// time-designation shape the store does not already hold. A store-keyed name — a seeded period or an
// operator-seeded exception such as `may 1968` — is never a drop.
func IsTimeDesignationDrop(name string, storeKeys map[string]struct{}) bool {
	if !IsTimeDesignation(name) {
		return false
	}
	if _, ok := storeKeys[Key(name)]; ok {
		return false
	}
	return true
}

func isMonthName(tok string) bool {
	_, ok := monthNames[tok]
	return ok
}

// isSpelledDecade reports whether the token is a spelled decade word ("sixties", "nineties"), which the
// gate treats like the numeric decade form because it names a span, not a dimension.
func isSpelledDecade(tok string) bool {
	spelled := []string{"twenties", "thirties", "forties", "fifties", "sixties", "seventies", "eighties", "nineties"}
	return slices.Contains(spelled, tok)
}

// isYearToken reports whether the token is a bare year: three or four digits.
func isYearToken(tok string) bool {
	if len(tok) < 3 || len(tok) > 4 {
		return false
	}
	return allDigits(tok)
}

// isDecadeToken reports whether the token is a numeric decade: two to four digits and a trailing 's'.
func isDecadeToken(tok string) bool {
	if len(tok) < 3 || len(tok) > 5 || !strings.HasSuffix(tok, "s") {
		return false
	}
	return allDigits(tok[:len(tok)-1])
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}
