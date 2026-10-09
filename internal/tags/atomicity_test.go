package tags

import "testing"

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
// otherwise be dropped as no_dimension (report N1).
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
