package textnorm

import (
	"reflect"
	"testing"
)

func TestSplitJointTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want []string
	}{
		{"empty string", "", nil},
		{"single word", "physics", nil},
		{"two words no conjunction", "machine learning", nil},
		{"and", "and", nil},
		{"or", "or", nil},
		{"leading conjunction", "and supply demand", nil},
		{"trailing conjunction", "supply and", nil},
		{"double conjunction", "a and or b", nil},
		{"basic and", "supply and demand", []string{"supply", "demand"}},
		{"basic or", "capitalism or socialism", []string{"capitalism", "socialism"}},
		{"basic vs", "left vs right", []string{"left", "right"}},
		{"basic vs.", "left vs. right", []string{"left", "right"}},
		{"basic versus", "left versus right", []string{"left", "right"}},
		{"three segments", "a and b and c", []string{"a", "b", "c"}},
		{"multi-word segments", "rock and roll", []string{"rock", "roll"}},
		{"mixed case", "Supply And Demand", []string{"Supply", "Demand"}},
		{"lowercase normalization", "supply AND demand", []string{"supply", "demand"}},
		{"vs without trailing", "capitalism vs socialism", []string{"capitalism", "socialism"}},
		{"four segments", "w x and y z", []string{"w x", "y z"}},
		{"single conjunction only", "and", nil},
		{"conjunction at start of multi", "and b", nil},
		{"or only two words", "a or", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitJointTag(tt.tag)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitJointTag(%q) = %v, want %v", tt.tag, got, tt.want)
			}
		})
	}
}
