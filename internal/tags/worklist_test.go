package tags

import "testing"

func overlapRecord(a, b string, jaccard, containmentA, containmentB float64, massA, massB int64) Record {
	return Record{
		Operator: OperatorOverlap,
		Operands: []string{a, b},
		Numbers: map[string]any{
			NumberJaccard:      jaccard,
			NumberContainmentA: containmentA,
			NumberContainmentB: containmentB,
			NumberMassA:        massA,
			NumberMassB:        massB,
		},
		State: StateSufficient,
	}
}

func reconstructRecord(compound string, excessRatio, deficitRatio float64, state EvidenceState) Record {
	return Record{
		Operator: OperatorReconstructibility,
		Operands: []string{compound, "part"},
		Numbers: map[string]any{
			NumberExcessRatio:  excessRatio,
			NumberDeficitRatio: deficitRatio,
		},
		State: state,
	}
}

func TestDeriveWorklists_Twins(t *testing.T) {
	cases := []struct {
		name      string
		jaccard   float64
		massA     int64
		massB     int64
		wantTwins int
		wantGray  int
	}{
		{"clear twin", 0.95, 4, 3, 1, 0},
		{"borderline gray", 0.8, 4, 3, 0, 1},
		{"below the floor", 0.65, 4, 3, 0, 0},
		{"mass floor applies to gray too", 0.8, 1, 3, 0, 0},
		{"mass floor drops a mass-2 pair", 0.95, 2, 2, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := DeriveWorklists([]Record{overlapRecord("a", "b", tc.jaccard, 0.1, 0.1, tc.massA, tc.massB)})

			gray, clear := 0, 0
			for _, twin := range w.Twins {
				if twin.Gray {
					gray++
				} else {
					clear++
				}
			}
			if clear != tc.wantTwins || gray != tc.wantGray {
				t.Errorf("twins = %d clear, %d gray; want %d clear, %d gray", clear, gray, tc.wantTwins, tc.wantGray)
			}
		})
	}
}

func TestDeriveWorklists_Siblings(t *testing.T) {
	cases := []struct {
		name         string
		containmentA float64
		containmentB float64
		massA        int64
		massB        int64
		wantSiblings int
		wantGray     int
	}{
		{"redundant", 0.9, 0.2, 4, 3, 1, 0},
		{"borderline gray", 0.7, 0.2, 4, 3, 0, 1},
		{"too weak", 0.5, 0.5, 4, 3, 0, 0},
		{"redundancy counts from either direction", 0.2, 0.85, 4, 3, 1, 0},
		{"mass floor drops a mass-2 pair", 0.9, 0.2, 2, 2, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := DeriveWorklists([]Record{overlapRecord("a", "b", 0.5, tc.containmentA, tc.containmentB, tc.massA, tc.massB)})

			gray, clear := 0, 0
			for _, sib := range w.Siblings {
				if sib.Gray {
					gray++
				} else {
					clear++
				}
			}
			if clear != tc.wantSiblings || gray != tc.wantGray {
				t.Errorf("siblings = %d clear, %d gray; want %d clear, %d gray", clear, gray, tc.wantSiblings, tc.wantGray)
			}
		})
	}
}

func TestDeriveWorklists_CompoundSplits(t *testing.T) {
	cases := []struct {
		name       string
		excess     float64
		deficit    float64
		state      EvidenceState
		wantSplits int
		wantGray   int
	}{
		{"both leftovers small", 0.05, 0.05, StateSufficient, 1, 0},
		{"inside the gray band", 0.2, 0.05, StateSufficient, 0, 1},
		{"deficit too large", 0.05, 0.3, StateSufficient, 0, 0},
		{"no reading, no proposal", 0.05, 0.05, StateInsufficient, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := DeriveWorklists([]Record{reconstructRecord("history of china", tc.excess, tc.deficit, tc.state)})

			gray, clear := 0, 0
			for _, split := range w.CompoundSplits {
				if split.Gray {
					gray++
				} else {
					clear++
				}
			}
			if clear != tc.wantSplits || gray != tc.wantGray {
				t.Errorf("splits = %d clear, %d gray; want %d clear, %d gray", clear, gray, tc.wantSplits, tc.wantGray)
			}
		})
	}
}

func TestDeriveWorklists_CompoundCarriesParts(t *testing.T) {
	record := reconstructRecord("history of china", 0.05, 0.05, StateSufficient)
	record.Operands = []string{"history of china", "history", "china"}

	w := DeriveWorklists([]Record{record})

	if len(w.CompoundSplits) != 1 {
		t.Fatalf("CompoundSplits = %d, want 1", len(w.CompoundSplits))
	}
	got := w.CompoundSplits[0].Parts
	if len(got) != 2 || got[0] != "history" || got[1] != "china" {
		t.Errorf("Parts = %v, want [history china]", got)
	}
}

func TestClassifyReconstructibility(t *testing.T) {
	cases := []struct {
		name    string
		state   EvidenceState
		excess  float64
		deficit float64
		want    ReconstructibilityBand
	}{
		{"an insufficient extension supports no band", StateInsufficient, 0, 0, ReconstructibilityInsufficient},
		{"an excess beyond the gray cut is far", StateSufficient, CompoundGrayCut + 0.05, 0, ReconstructibilityFar},
		{"a deficit beyond the gray cut is far", StateSufficient, 0, CompoundGrayCut + 0.05, ReconstructibilityFar},
		{"an excess at the gray cut still reviews", StateSufficient, CompoundGrayCut, 0, ReconstructibilityGray},
		{"an excess above the split cut reviews", StateSufficient, CompoundSplitCut + 0.05, 0, ReconstructibilityGray},
		{"an excess at the split cut is firm", StateSufficient, CompoundSplitCut, 0.05, ReconstructibilityFirm},
		{"a tight intersection on both ratios is firm", StateSufficient, 0, 0, ReconstructibilityFirm},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyReconstructibility(tc.state, tc.excess, tc.deficit)
			if got != tc.want {
				t.Errorf("ClassifyReconstructibility(%v, %v, %v) = %q, want %q",
					tc.state, tc.excess, tc.deficit, got, tc.want)
			}
		})
	}
}

func TestDeriveWorklists_SiblingDirection(t *testing.T) {
	cases := []struct {
		name         string
		containmentA float64
		containmentB float64
		massA        int64
		massB        int64
		wantA        string
		wantB        string
		wantMassA    int64
		wantMassB    int64
		wantGray     bool
	}{
		{"the broader tag leads when the second operand contains the first", 0.9, 0.2, 4, 9, "b", "a", 9, 4, false},
		{"the broader tag leads when the first operand contains the second", 0.2, 0.85, 9, 4, "a", "b", 9, 4, false},
		{"equal containment keeps operand order and marks the review band", 0.7, 0.7, 5, 5, "a", "b", 5, 5, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := DeriveWorklists([]Record{overlapRecord("a", "b", 0.5, tc.containmentA, tc.containmentB, tc.massA, tc.massB)})
			if len(w.Siblings) != 1 {
				t.Fatalf("Siblings = %d, want 1", len(w.Siblings))
			}
			got := w.Siblings[0]
			if got.A != tc.wantA || got.B != tc.wantB || got.MassA != tc.wantMassA || got.MassB != tc.wantMassB || got.Gray != tc.wantGray {
				t.Errorf("Sibling = {%s ⊇ %s masses %d/%d gray=%t}, want {%s ⊇ %s masses %d/%d gray=%t}",
					got.A, got.B, got.MassA, got.MassB, got.Gray,
					tc.wantA, tc.wantB, tc.wantMassA, tc.wantMassB, tc.wantGray)
			}
		})
	}
}
