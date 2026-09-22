package tags

import (
	"math"
	"testing"
)

func almost(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestSetOperations(t *testing.T) {
	a := NewSet(1, 2, 3)
	b := NewSet(3, 4)

	if got := a.Size(); got != 3 {
		t.Errorf("Size = %d, want 3", got)
	}
	if !a.Contains(2) || a.Contains(4) {
		t.Error("Contains does not match membership")
	}
	if got := a.Union(b).Size(); got != 4 {
		t.Errorf("Union size = %d, want 4", got)
	}
	if got := a.Intersect(b).Size(); got != 1 || !a.Intersect(b).Contains(3) {
		t.Errorf("Intersect = %v, want {3}", a.Intersect(b))
	}
	if got := a.Difference(b).Size(); got != 2 || a.Difference(b).Contains(3) {
		t.Errorf("Difference = %v, want {1 2}", a.Difference(b))
	}
}

func TestNewOverlapReading(t *testing.T) {
	cases := []struct {
		name         string
		massA, massB int64
		intersection int64
		union        int64
		jaccard      float64
		overlap      float64
		containmentA float64
		containmentB float64
		state        EvidenceState
	}{
		{"identical extensions", 3, 3, 3, 3, 1, 1, 1, 1, StateSufficient},
		{"one contained in the other", 4, 3, 3, 4, 0.75, 1, 0.75, 1, StateSufficient},
		{"disjoint extensions", 2, 3, 0, 5, 0, 0, 0, 0, StateSufficient},
		{"empty extension", 4, 0, 0, 4, 0, 0, 0, 0, StateInsufficient},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewOverlapReading("a", "b", tc.massA, tc.massB, tc.intersection)
			if r.Intersection != tc.intersection {
				t.Errorf("Intersection = %d, want %d", r.Intersection, tc.intersection)
			}
			if r.Union != tc.union {
				t.Errorf("Union = %d, want %d", r.Union, tc.union)
			}
			if !almost(r.Jaccard, tc.jaccard) {
				t.Errorf("Jaccard = %v, want %v", r.Jaccard, tc.jaccard)
			}
			if !almost(r.OverlapCoefficient, tc.overlap) {
				t.Errorf("OverlapCoefficient = %v, want %v", r.OverlapCoefficient, tc.overlap)
			}
			if !almost(r.ContainmentA, tc.containmentA) {
				t.Errorf("ContainmentA = %v, want %v", r.ContainmentA, tc.containmentA)
			}
			if !almost(r.ContainmentB, tc.containmentB) {
				t.Errorf("ContainmentB = %v, want %v", r.ContainmentB, tc.containmentB)
			}
			if r.State != tc.state {
				t.Errorf("State = %q, want %q", r.State, tc.state)
			}
		})
	}
}

func TestOverlapOfAgreesWithCounts(t *testing.T) {
	fromSets := OverlapOf("a", "b", NewSet(1, 2, 3), NewSet(2, 3, 4))
	fromCounts := NewOverlapReading("a", "b", 3, 3, 2)
	if fromSets.Intersection != fromCounts.Intersection || !almost(fromSets.Jaccard, fromCounts.Jaccard) {
		t.Errorf("set path = %+v, count path = %+v", fromSets, fromCounts)
	}
	if !almost(fromSets.Jaccard, 0.5) {
		t.Errorf("Jaccard = %v, want 0.5", fromSets.Jaccard)
	}
}

func TestNewReconstructibilityReading(t *testing.T) {
	cases := []struct {
		name              string
		compoundMass      int64
		partsIntersection int64
		both              int64
		excess            int64
		deficit           int64
		excessRatio       float64
		deficitRatio      float64
		state             EvidenceState
	}{
		{"tight intersection", 3, 3, 3, 0, 0, 0, 0, StateSufficient},
		{"both leftovers present", 2, 2, 1, 1, 1, 0.5, 0.5, StateSufficient},
		{"phrase without live documents", 0, 0, 0, 0, 0, 0, 0, StateInsufficient},
		{"missing part empties the intersection", 1, 0, 0, 1, 0, 1, 0, StateInsufficient},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReconstructibilityReading("compound", []string{"p"}, tc.compoundMass, tc.partsIntersection, tc.both)
			if r.Excess != tc.excess {
				t.Errorf("Excess = %d, want %d", r.Excess, tc.excess)
			}
			if r.Deficit != tc.deficit {
				t.Errorf("Deficit = %d, want %d", r.Deficit, tc.deficit)
			}
			if !almost(r.ExcessRatio, tc.excessRatio) {
				t.Errorf("ExcessRatio = %v, want %v", r.ExcessRatio, tc.excessRatio)
			}
			if !almost(r.DeficitRatio, tc.deficitRatio) {
				t.Errorf("DeficitRatio = %v, want %v", r.DeficitRatio, tc.deficitRatio)
			}
			if r.State != tc.state {
				t.Errorf("State = %q, want %q", r.State, tc.state)
			}
		})
	}
}

func TestReconstructibilityOf(t *testing.T) {
	reading := ReconstructibilityOf("bike repair", []string{"bike", "repair"},
		NewSet(1, 5), []Set{NewSet(1, 4), NewSet(1, 4, 5)})

	if reading.CompoundMass != 2 {
		t.Errorf("CompoundMass = %d, want 2", reading.CompoundMass)
	}
	if reading.PartsIntersection != 2 {
		t.Errorf("PartsIntersection = %d, want 2", reading.PartsIntersection)
	}
	if reading.Both != 1 {
		t.Errorf("Both = %d, want 1", reading.Both)
	}
	if reading.Excess != 1 || reading.Deficit != 1 {
		t.Errorf("Excess/Deficit = %d/%d, want 1/1", reading.Excess, reading.Deficit)
	}
}

func TestNewPartCoverageReading(t *testing.T) {
	reading := NewPartCoverageReading("bike repair", "bike", 4, 10, 2, 5)
	if !almost(reading.Coverage, 0.5) {
		t.Errorf("Coverage = %v, want 0.5", reading.Coverage)
	}
	if reading.State != StateSufficient {
		t.Errorf("State = %q, want %q", reading.State, StateSufficient)
	}

	missing := NewPartCoverageReading("bike repair", "ritual", 4, 0, 0, 0)
	if missing.State != StateInsufficient {
		t.Errorf("missing part State = %q, want %q", missing.State, StateInsufficient)
	}
}

func TestMassOf(t *testing.T) {
	if got := MassOf("a", NewSet(1, 2)).State; got != StateSufficient {
		t.Errorf("non-empty mass State = %q, want %q", got, StateSufficient)
	}
	if got := MassOf("a", Set{}).State; got != StateInsufficient {
		t.Errorf("empty mass State = %q, want %q", got, StateInsufficient)
	}
}

func TestNewEqualityReading(t *testing.T) {
	r := NewEqualityReading("a", "b", 3, 3, 3)
	if !almost(r.SetSimilarity, 1) {
		t.Errorf("SetSimilarity = %v, want 1", r.SetSimilarity)
	}
	if r.State != StateSufficient {
		t.Errorf("State = %q, want %q", r.State, StateSufficient)
	}

	empty := NewEqualityReading("a", "b", 0, 3, 0)
	if empty.State != StateInsufficient {
		t.Errorf("empty State = %q, want %q", empty.State, StateInsufficient)
	}
}

func TestNewComplementReading(t *testing.T) {
	if got := NewComplementReading("a", "b", 5, 3); got.Size != 2 {
		t.Errorf("Size = %d, want 2", got.Size)
	}
	if got := NewComplementReading("a", "b", 0, 0); got.State != StateInsufficient {
		t.Errorf("State = %q, want %q", got.State, StateInsufficient)
	}
}

func TestNewContainmentReading(t *testing.T) {
	reading := NewContainmentReading("container", "contained", 4, 3, 3)
	if !almost(reading.Ratio, 1) {
		t.Errorf("Ratio = %v, want 1", reading.Ratio)
	}
	if reading.State != StateSufficient {
		t.Errorf("State = %q, want %q", reading.State, StateSufficient)
	}
	if got := NewContainmentReading("container", "contained", 4, 0, 0); got.State != StateInsufficient {
		t.Errorf("empty State = %q, want %q", got.State, StateInsufficient)
	}
}
