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
