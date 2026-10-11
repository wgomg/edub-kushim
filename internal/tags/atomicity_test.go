package tags

import (
	"reflect"
	"testing"
)

func TestClassifyShape(t *testing.T) {
	cases := []struct {
		name string
		want Shape
	}{
		{"history", ShapeSingleWord},
		{"bike repair", ShapeCompound},
		{"history of china", ShapeCompound},
		{"a very long tag name", ShapeTooLong},
		{"история китая", ShapeNonEnglish},
		{"of the", ShapeNoParts},
		{"", ShapeNoParts},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyShape(tc.name); got != tc.want {
				t.Errorf("ClassifyShape(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestDeriveParts(t *testing.T) {
	cases := []struct {
		name string
		in   []string
	}{
		{"history of china", []string{"history", "china"}},
		{"bike repair", []string{"bike", "repair"}},
		{"the war of the worlds", []string{"war", "worlds"}},
		{"modern rural exodus", []string{"modern", "rural", "exodus"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveParts(tc.name)
			if len(got) != len(tc.in) {
				t.Fatalf("DeriveParts(%q) = %v, want %v", tc.name, got, tc.in)
			}
			for i := range got {
				if got[i] != tc.in[i] {
					t.Errorf("DeriveParts(%q) = %v, want %v", tc.name, got, tc.in)
					break
				}
			}
		})
	}
}

// TestIsPeriodClass pins the split gate's deterministic class: only a house numeric period — an ordinal
// token followed by one of the three pinned unit nouns — qualifies, and a spelled ordinal reaches it
// only through the house form the caller applies. Anything else stays on the judged path, where it could
// otherwise be dropped as no_dimension.
func TestIsPeriodClass(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"ordinal century", "20th century", true},
		{"ordinal decade", "3rd decade", true},
		{"ordinal millennium", "1000th millennium", true},
		{"a spelled ordinal is not the raw shape", "twentieth century", false},
		{"the house form reaches the class", HouseForm("twentieth century"), true},
		{"a modifier breaks the two-token shape", "early 20th century", false},
		{"a plural unit is not a pinned unit", "20 centuries", false},
		{"a bare ordinal is not a period", "20th", false},
		{"non-Latin script never qualifies", "19 век", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPeriodClass(tc.input); got != tc.want {
				t.Errorf("IsPeriodClass(%q) = %t, want %t", tc.input, got, tc.want)
			}
		})
	}
}

// TestDeriveSegments pins the decomposition fan a compound-shaped name produces: every content word plus
// every contiguous bigram, singles first, so the cover scan can prefer the longest segment without
// inventing a trigram the gate would then have to reject.
func TestDeriveSegments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"a single word fans to itself", "history", []string{"history"}},
		{"a two-word compound adds the bigram", "bike repair", []string{"bike", "repair", "bike repair"}},
		{"function words are stripped before the fan", "history of china", []string{"history", "china", "history china"}},
		{
			"three content words fan singles then bigrams",
			"object oriented programming",
			[]string{"object", "oriented", "programming", "object oriented", "oriented programming"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveSegments(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DeriveSegments(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// usableSpans resolves the named spans to themselves and rejects everything else, the resolver shape the
// cover scan consumes.
func usableSpans(spans ...string) func(string) (string, bool) {
	usable := make(map[string]struct{}, len(spans))
	for _, span := range spans {
		usable[span] = struct{}{}
	}
	return func(span string) (string, bool) {
		if _, ok := usable[span]; ok {
			return span, true
		}
		return "", false
	}
}

// TestGreedyCover pins the coarsest-cover rule the split gate disposes by: the longest span at each
// position, leftmost first, applied names carried into the cover; a span covering the whole sequence is
// never a segment, so a two-word compound decomposes into its two words or keeps whole; and a word no span
// covers fails a full cover while alongside still returns the surviving partial cover.
func TestGreedyCover(t *testing.T) {
	cases := []struct {
		name      string
		content   []string
		resolve   func(string) (string, bool)
		alongside bool
		want      []string
	}{
		{
			name:    "the longest span at each position wins",
			content: []string{"spanish", "civil", "war"},
			resolve: usableSpans("spanish", "civil war"),
			want:    []string{"spanish", "civil war"},
		},
		{
			name:    "the leftmost bigram wins when two bigrams are usable",
			content: []string{"geographic", "information", "system"},
			resolve: usableSpans("geographic information", "information system", "system"),
			want:    []string{"geographic information", "system"},
		},
		{
			name:    "a whole-sequence bigram is never a segment",
			content: []string{"civil", "war"},
			resolve: usableSpans("civil", "war", "civil war"),
			want:    []string{"civil", "war"},
		},
		{
			name:    "a whole-sequence bigram alone is not a decomposition",
			content: []string{"black", "bloc"},
			resolve: usableSpans("black bloc"),
			want:    nil,
		},
		{
			name:    "an uncovered word fails a full cover",
			content: []string{"freedom", "speech"},
			resolve: usableSpans("freedom"),
			want:    nil,
		},
		{
			name:      "alongside skips the words no span covers",
			content:   []string{"world", "war", "2"},
			resolve:   usableSpans("war"),
			alongside: true,
			want:      []string{"war"},
		},
		{
			name:    "the same gap fails a full cover",
			content: []string{"world", "war", "2"},
			resolve: usableSpans("war"),
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GreedyCover(tc.content, tc.resolve, tc.alongside); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("GreedyCover(%v, alongside=%t) = %v, want %v", tc.content, tc.alongside, got, tc.want)
			}
		})
	}
}

// TestIsTimeDesignation pins the deterministic shape the time gate drops: the bare time forms that are not
// subject dimensions, and the ordinary names that are not them.
func TestIsTimeDesignation(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"a bare year", "1968", true},
		{"a numeric decade", "1940s", true},
		{"a two-digit decade", "60s", true},
		{"a month name", "october", true},
		{"a month that is also a modal", "may", true},
		{"a month and a year", "may 1968", true},
		{"an ordinal period unit", "12th century", true},
		{"a spelled ordinal period is not the shape", "twentieth century", false},
		{"an ordinary subject word is not a designation", "revolution", false},
		{"a year inside a longer name is not a designation", "world war 2", false},
		{"a bare unit noun is not a designation", "century", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTimeDesignation(tc.input); got != tc.want {
				t.Errorf("IsTimeDesignation(%q) = %t, want %t", tc.input, got, tc.want)
			}
		})
	}
}

// TestIsTimeDesignationDrop pins the store exemption: the shape is a drop only while the live view does not
// hold the name, so a seeded period or an operator-seeded exception passes the gate.
func TestIsTimeDesignationDrop(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		storeKeys map[string]struct{}
		want      bool
	}{
		{"an unseeded period is a drop", "12th century", nil, true},
		{"a seeded period is never a drop", "20th century", map[string]struct{}{Key("20th century"): {}}, false},
		{"a seeded month-year is never a drop", "may 1968", map[string]struct{}{Key("may 1968"): {}}, false},
		{"an ordinary word is not a drop", "revolution", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTimeDesignationDrop(tc.input, tc.storeKeys); got != tc.want {
				t.Errorf("IsTimeDesignationDrop(%q) = %t, want %t", tc.input, got, tc.want)
			}
		})
	}
}
