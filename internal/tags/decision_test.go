package tags

import "testing"

func TestDecideAdmission(t *testing.T) {
	admissible := AdmissionFeatures{SubjectMatter: true, Dimension: true, NotPersonPlaceWork: true, Atomic: true, NotFormGenre: true}

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
		{"two failing rubric answers reject", func() AdmissionFeatures {
			f := admissible
			f.Dimension = false
			f.Atomic = false
			return f
		}(), AdmissionReject, ReasonCorroborated},
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
		{"a lone deterministic signal defers", func() AdmissionFeatures {
			f := admissible
			f.CategoryOther = true
			return f
		}(), AdmissionDefer, ReasonSingleSignal},
		{"two deterministic signals reject", func() AdmissionFeatures {
			f := admissible
			f.CategoryOther = true
			f.NoAnchor = true
			return f
		}(), AdmissionReject, ReasonCorroborated},
		{"judge and deterministic signals corroborate", func() AdmissionFeatures {
			f := admissible
			f.SubjectMatter = false
			f.NoAnchor = true
			return f
		}(), AdmissionReject, ReasonCorroborated},
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
			if got := DecidePromotion(tc.outcome, tc.live); got != tc.want {
				t.Errorf("DecidePromotion(%v, %d) = %v, want %v", tc.outcome, tc.live, got, tc.want)
			}
		})
	}
}
