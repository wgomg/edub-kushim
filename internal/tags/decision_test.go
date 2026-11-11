package tags

import (
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/types"
)

func TestDecideAdmission(t *testing.T) {
	admissible := AdmissionFeatures{SubjectMatter: true, Dimension: true, PersonClass: types.PersonClassNotPerson, Atomic: true, NotFormGenre: true}

	cases := []struct {
		name        string
		features    AdmissionFeatures
		wantOutcome AdmissionOutcome
		wantReason  string
	}{
		{"no failing signal admits", admissible, AdmissionAdmit, ReasonAdmitted},
		{"a lone failing rubric answer defers to review", func() AdmissionFeatures {
			f := admissible
			f.Dimension = false
			return f
		}(), AdmissionDefer, ReasonSingleSignal},
		{"not_form_genre failing alone defers to review", func() AdmissionFeatures {
			f := admissible
			f.NotFormGenre = false
			return f
		}(), AdmissionDefer, ReasonSingleSignal},
		{"a lone subject-matter failure defers to review", func() AdmissionFeatures {
			f := admissible
			f.SubjectMatter = false
			return f
		}(), AdmissionDefer, ReasonSingleSignal},
		{"two failing rubric answers reject", func() AdmissionFeatures {
			f := admissible
			f.Dimension = false
			f.Atomic = false
			return f
		}(), AdmissionReject, ReasonCorroborated},
		{"a subject-matter and dimension failure reject as no_dimension", func() AdmissionFeatures {
			f := admissible
			f.SubjectMatter = false
			f.Dimension = false
			return f
		}(), AdmissionReject, ReasonNoDimension},
		{"judge abstention defers before counting", func() AdmissionFeatures {
			f := admissible
			f.JudgeDefer = true
			return f
		}(), AdmissionDefer, ReasonJudgeDefer},
		{"judge abstention wins over corroborated failures", func() AdmissionFeatures {
			f := admissible
			f.JudgeDefer = true
			f.SubjectMatter = false
			f.Atomic = false
			return f
		}(), AdmissionDefer, ReasonJudgeDefer},
		{"judge abstention wins over the no_dimension rule", func() AdmissionFeatures {
			f := admissible
			f.JudgeDefer = true
			f.SubjectMatter = false
			f.Dimension = false
			return f
		}(), AdmissionDefer, ReasonJudgeDefer},
		{"a lone deterministic signal defers", func() AdmissionFeatures {
			f := admissible
			f.CategoryOther = true
			return f
		}(), AdmissionDefer, ReasonSingleSignal},
		{"two deterministic signals defer to review", func() AdmissionFeatures {
			f := admissible
			f.CategoryOther = true
			f.NoAnchor = true
			return f
		}(), AdmissionDefer, ReasonDeterministicOnly},
		{"judge and deterministic signals corroborate", func() AdmissionFeatures {
			f := admissible
			f.SubjectMatter = false
			f.NoAnchor = true
			return f
		}(), AdmissionReject, ReasonCorroborated},
		{"a documented individual rejects as person_facet", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassRealIndividual
			return f
		}(), AdmissionReject, ReasonPersonFacet},
		{"person_facet does not depend on subject_matter", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassRealIndividual
			f.SubjectMatter = false
			return f
		}(), AdmissionReject, ReasonPersonFacet},
		{"a mythological class defers as person_review", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassMythologicalOrLegendary
			return f
		}(), AdmissionDefer, ReasonPersonReview},
		{"a fictional class defers as person_review", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassFictional
			return f
		}(), AdmissionDefer, ReasonPersonReview},
		{"an undecided class defers as person_review", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassUncertain
			return f
		}(), AdmissionDefer, ReasonPersonReview},
		{"a missing class defers as person_review", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = ""
			return f
		}(), AdmissionDefer, ReasonPersonReview},
		{"person_review outranks corroborated failures", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassMythologicalOrLegendary
			f.Atomic = false
			f.NotFormGenre = false
			return f
		}(), AdmissionDefer, ReasonPersonReview},
		{"judge abstention outranks person_facet", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassRealIndividual
			f.JudgeDefer = true
			return f
		}(), AdmissionDefer, ReasonJudgeDefer},
		{"no_dimension outranks person_facet", func() AdmissionFeatures {
			f := admissible
			f.PersonClass = types.PersonClassRealIndividual
			f.SubjectMatter = false
			f.Dimension = false
			return f
		}(), AdmissionReject, ReasonNoDimension},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideAdmission(tc.features)
			if got.Outcome != tc.wantOutcome {
				t.Errorf("outcome = %v, want %v (reason %q)", got.Outcome, tc.wantOutcome, got.Reason)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

func TestDecidePromotion(t *testing.T) {
	cases := []struct {
		name    string
		outcome AdmissionOutcome
		live    int64
		want    bool
	}{
		{"admitted below the floor stays pending", AdmissionAdmit, 9, false},
		{"admitted at the floor promotes", AdmissionAdmit, 10, true},
		{"admitted above the floor promotes", AdmissionAdmit, 11, true},
		{"deferred never promotes regardless of mass", AdmissionDefer, 11, false},
		{"rejected never promotes regardless of mass", AdmissionReject, 10, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecidePromotion(tc.outcome, tc.live, 10); got != tc.want {
				t.Errorf("DecidePromotion(%v, %d) = %v, want %v", tc.outcome, tc.live, got, tc.want)
			}
		})
	}
}

func TestDecideMassDemotion(t *testing.T) {
	cases := []struct {
		name string
		live int64
		want bool
	}{
		{"zero mass demotes", 0, true},
		{"one below the floor demotes", 10 - 1, true},
		{"mass at the floor keeps standing", 10, false},
		{"mass above the floor keeps standing", 10 + 1, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideMassDemotion(tc.live, 10); got != tc.want {
				t.Errorf("DecideMassDemotion(%d) = %v, want %v", tc.live, got, tc.want)
			}
		})
	}
}

// TestDecideRulesHonorConfiguredFloor pins that the floor argument decides, not a baked-in default:
// at a floor of 3 the promotion minimum and the demotion threshold move with it.
func TestDecideRulesHonorConfiguredFloor(t *testing.T) {
	if got := DecidePromotion(AdmissionAdmit, 3, 3); !got {
		t.Error("mass at a configured floor of 3 must promote")
	}
	if got := DecidePromotion(AdmissionAdmit, 2, 3); got {
		t.Error("mass below a configured floor of 3 must not promote")
	}
	if got := DecidePromotion(AdmissionDefer, 5, 3); got {
		t.Error("a deferred candidate never promotes, whatever the floor")
	}
	if got := DecideMassDemotion(2, 3); !got {
		t.Error("mass below a configured floor of 3 must demote")
	}
	if got := DecideMassDemotion(3, 3); got {
		t.Error("mass at a configured floor of 3 must keep standing")
	}
}

func TestDecideComposition(t *testing.T) {
	cases := []struct {
		name     string
		features CompositionFeatures
		want     CompositionDecision
	}{
		{
			"a fresh-sample disagreement defers before any branch is read",
			CompositionFeatures{
				SampleDisagreement:  true,
				JudgeUnavailable:    true,
				CompoundComposition: true,
				PartsDimensional:    true,
			},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSampleDisagreement, Hold: true},
		},
		{
			"an unavailable judge keeps and holds until a later verdict",
			CompositionFeatures{JudgeUnavailable: true, CompoundDeferred: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonJudgeUnavailable, Hold: true},
		},
		{
			"a compound abstention holds ahead of the split branch",
			CompositionFeatures{CompoundDeferred: true, CompoundComposition: true, PartsDimensional: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSplitDefer, Hold: true},
		},
		{
			"a parts abstention holds ahead of the split branch",
			CompositionFeatures{PartsDeferred: true, CompoundComposition: true, PartsDimensional: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSplitDefer, Hold: true},
		},
		{
			"a composition contradicting the parts goes to review",
			CompositionFeatures{CompoundComposition: true, PartsNotDimensional: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonConflictingEvidence, Hold: true},
		},
		{
			"a composition over dimensional parts splits",
			CompositionFeatures{CompoundComposition: true, PartsDimensional: true},
			CompositionDecision{Disposition: CompositionSplit},
		},
		{
			"a far-from-intersection composition holds as a relational keep instead of splitting",
			CompositionFeatures{CompoundComposition: true, PartsDimensional: true, FarFromIntersection: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonRelationalKeep, Hold: true},
		},
		{
			"reconstructibility does not divert the composition branch",
			CompositionFeatures{CompoundComposition: true, PartsDimensional: true, Reconstructible: true},
			CompositionDecision{Disposition: CompositionSplit},
		},
		{
			"a no_dimension verdict agreeing with the parts drops and holds promotion",
			CompositionFeatures{CompoundNoDimension: true, PartsNotDimensional: true},
			CompositionDecision{Disposition: CompositionDrop, Reason: CompositionReasonNoDimension, Hold: true},
		},
		{
			"a no_dimension verdict contradicting the parts goes to review",
			CompositionFeatures{CompoundNoDimension: true, PartsDimensional: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonConflictingEvidence, Hold: true},
		},
		{
			"a named unit over a non-dimensional part keeps",
			CompositionFeatures{CompoundNamedUnit: true, PartsNotDimensional: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonNamedUnit},
		},
		{
			"a reconstructible named unit keeps and leaves standing to the floor",
			CompositionFeatures{CompoundNamedUnit: true, PartsDimensional: true, Reconstructible: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonNamedUnitReconstructible},
		},
		{
			"a named unit over dimensional parts holds as a relational keep",
			CompositionFeatures{CompoundNamedUnit: true, PartsDimensional: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonRelationalKeep, Hold: true},
		},
		{
			"a compound status no part verdict can arbitrate falls to the fail-safe",
			CompositionFeatures{CompoundNamedUnit: true},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSplitDefer, Hold: true},
		},
		{
			"an empty feature set falls to the fail-safe",
			CompositionFeatures{},
			CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSplitDefer, Hold: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideComposition(tc.features)
			if got.Disposition != tc.want.Disposition || got.Reason != tc.want.Reason || got.Hold != tc.want.Hold {
				t.Errorf("DecideComposition(%+v) = {Disposition:%q Reason:%q Hold:%t}, want {Disposition:%q Reason:%q Hold:%t}",
					tc.features, got.Disposition, got.Reason, got.Hold,
					tc.want.Disposition, tc.want.Reason, tc.want.Hold)
			}
		})
	}
}

func TestWithAcceptance(t *testing.T) {
	accepted := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		derived    AdmissionDecision
		acceptedAt time.Time
		want       AdmissionDecision
	}{
		{
			name:    "no acceptance keeps the derived decision",
			derived: AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonSingleSignal},
			want:    AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonSingleSignal},
		},
		{
			name:       "a stamped acceptance waives any defer",
			derived:    AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonSingleSignal},
			acceptedAt: accepted,
			want:       AdmissionDecision{Outcome: AdmissionAdmit, Reason: ReasonOperatorAccept},
		},
		{
			name:       "an acceptance never waives a rejection",
			derived:    AdmissionDecision{Outcome: AdmissionReject, Reason: ReasonCorroborated},
			acceptedAt: accepted,
			want:       AdmissionDecision{Outcome: AdmissionReject, Reason: ReasonCorroborated},
		},
		{
			name:       "an acceptance never overrides an admission",
			derived:    AdmissionDecision{Outcome: AdmissionAdmit, Reason: ReasonAdmitted},
			acceptedAt: accepted,
			want:       AdmissionDecision{Outcome: AdmissionAdmit, Reason: ReasonAdmitted},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WithAcceptance(tc.derived, tc.acceptedAt)
			if got != tc.want {
				t.Errorf("WithAcceptance(...) = {Outcome:%v Reason:%q}, want {Outcome:%v Reason:%q}",
					got.Outcome, got.Reason, tc.want.Outcome, tc.want.Reason)
			}
		})
	}
}
