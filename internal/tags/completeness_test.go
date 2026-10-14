package tags

import (
	"reflect"
	"testing"
)

// TestResolvedSegments pins the applied-store form coverage accounting reads, which is also the form the
// emission gate resolves: a fold lands on its anchor, a time designation the store does not hold is
// dropped with its bigrams, and an accepted bigram is literal.
func TestResolvedSegments(t *testing.T) {
	cases := []struct {
		name     string
		compound string
		promoted map[string]string
		live     map[string]struct{}
		want     []string
	}{
		{
			name:     "an attested fold lands on its anchor",
			compound: "mexican revolution",
			promoted: map[string]string{Key("mexico"): "mexico"},
			live:     map[string]struct{}{Key("mexico"): {}},
			want:     []string{"mexico", "revolution", "mexican revolution"},
		},
		{
			name:     "a store-held time bigram is kept",
			compound: "may 1968 celebration",
			live:     map[string]struct{}{Key("may"): {}, Key("1968"): {}, Key("may 1968"): {}},
			want:     []string{"may", "1968", "celebration", "may 1968", "1968 celebration"},
		},
		{
			name:     "a time bigram the store lacks is dropped",
			compound: "may 1968 celebration",
			live:     map[string]struct{}{Key("may"): {}, Key("1968"): {}},
			want:     []string{"may", "1968", "celebration", "1968 celebration"},
		},
		{
			name:     "an unseeded time word drops with its bigrams",
			compound: "october revolution",
			want:     []string{"revolution"},
		},
		{
			name:     "function words are not segments",
			compound: "the war of the worlds",
			want:     []string{"war", "worlds", "war worlds"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolvedSegments(tc.compound, tc.promoted, tc.live)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ResolvedSegments(%q) = %v, want %v", tc.compound, got, tc.want)
			}
		})
	}
}

// TestResolvedSegments_PeriodCarrierIsWhole pins the period exemption the gate applies: a seeded period
// decomposes into nothing, so a document carrying it cannot report its words as pending splits.
func TestResolvedSegments_PeriodCarrierIsWhole(t *testing.T) {
	if got := ResolvedSegments("19th century", nil, nil); len(got) != 0 {
		t.Errorf("ResolvedSegments(\"19th century\") = %v, want no segments: a period does not decompose", got)
	}
}

// TestCoversDimension pins the pending-split reading: a flagged dimension is pending-split exactly when the
// carrier's cover resolves to it, and a period carrier never covers — it will never decompose.
func TestCoversDimension(t *testing.T) {
	promoted := map[string]string{Key("mexico"): "mexico"}
	live := map[string]struct{}{Key("mexico"): {}}

	cases := []struct {
		name     string
		compound string
		dim      string
		want     bool
	}{
		{"a folded word covers its anchor", "mexican revolution", "mexico", true},
		{"a sibling word covers itself", "mexican revolution", "revolution", true},
		{"an unrelated dimension is not covered", "mexican revolution", "russia", false},
		{"a period carrier covers nothing", "19th century", "century", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CoversDimension(tc.compound, tc.dim, promoted, live); got != tc.want {
				t.Errorf("CoversDimension(%q, %q) = %t, want %t", tc.compound, tc.dim, got, tc.want)
			}
		})
	}
}

// TestPendingCoverageSegments pins the durable D35 class: a segment counts as pending coverage only when
// the store does not hold it as a value and no cached parts verdict already judged it.
func TestPendingCoverageSegments(t *testing.T) {
	promoted := map[string]string{Key("mexico"): "mexico"}
	live := map[string]struct{}{Key("mexico"): {}, Key("revolution"): {}}

	// the folded anchor and its sibling are store values, so only the bigram is a candidate
	got := PendingCoverageSegments("mexican revolution", promoted, live, nil)
	if !reflect.DeepEqual(got, []string{"mexican revolution"}) {
		t.Errorf("PendingCoverageSegments = %v, want the bigram that is neither a value nor judged", got)
	}

	// a cached parts verdict settles the bigram, so the carrier leaves no durable gap behind
	if got := PendingCoverageSegments("mexican revolution", promoted, live, map[string]struct{}{"mexican revolution": {}}); len(got) != 0 {
		t.Errorf("a judged carrier must report nothing pending, got %v", got)
	}
}

// TestTwinPartner pins the pending-canonicalization reading, which must be symmetric: the worklist pair
// names the two sides in either order.
func TestTwinPartner(t *testing.T) {
	twins := []Twin{{A: "russia", B: "soviet union"}}

	if !TwinPartner(twins, "soviet union", "russia") {
		t.Error("the pair must match with its sides swapped")
	}
	if TwinPartner(twins, "russia", "china") {
		t.Error("an unrelated dimension must not pair")
	}
}
