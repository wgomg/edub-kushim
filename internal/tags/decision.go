package tags

// AdmissionOutcome is how the pinned rule set disposes of one candidate.
// Reject drops it, defer sends it to review, and admit keeps it; defer and
// admit both leave the candidate provisional, told apart only by the
// promotion path.
type AdmissionOutcome int

// The three dispositions; reject is the zero value, so an outcome that
// was never set reads as a rejection.
const (
	AdmissionReject AdmissionOutcome = iota
	AdmissionDefer
	AdmissionAdmit
)

// String names the outcome for the audit logs.
func (o AdmissionOutcome) String() string {
	switch o {
	case AdmissionReject:
		return "reject"
	case AdmissionDefer:
		return "defer"
	case AdmissionAdmit:
		return "admit"
	default:
		return "unknown"
	}
}

// AdmissionFeatures is the evidence the admission decision reads for one
// candidate: the judge's five answers plus its abstention, and the
// deterministic signals the emission context supplies. A feature the
// caller cannot establish stays false and never counts as failing —
// absence of evidence is not evidence.
type AdmissionFeatures struct {
	SubjectMatter bool
	Dimension     bool
	NotPerson     bool
	Atomic        bool
	NotFormGenre  bool
	JudgeDefer    bool
	CategoryOther bool
	NoAnchor      bool
}

// The reasons an admission decision carries into the logs: a judge
// abstention, two or more failing signals, one lone failing signal in the
// gray band, or no failing signal.
const (
	ReasonJudgeDefer   = "judge_defer"
	ReasonCorroborated = "corroborated_failures"
	ReasonSingleSignal = "single_signal"
	ReasonAdmitted     = "admitted"
)

// AdmissionDecision carries the disposition and the named reason that
// drove it: one of this package's Reason* constants, or an interim path's
// own reason.
type AdmissionDecision struct {
	Outcome AdmissionOutcome
	Reason  string
}

// DecideAdmission decides one candidate from its features: evidence, not
// verdicts — no single signal decides. A judge abstention defers;
// otherwise two or more failing signals reject (corroborated evidence),
// exactly one defers to review (the gray band refuses single-signal
// drops), and none admits. The rule set is pinned and versioned — the
// quality version pins the judge features behind it — and runs with zero
// labels.
func DecideAdmission(f AdmissionFeatures) AdmissionDecision {
	if f.JudgeDefer {
		return AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonJudgeDefer}
	}
	failures := 0
	for _, failed := range []bool{
		!f.SubjectMatter,
		!f.Dimension,
		!f.NotPerson,
		!f.Atomic,
		!f.NotFormGenre,
		f.CategoryOther,
		f.NoAnchor,
	} {
		if failed {
			failures++
		}
	}
	switch {
	case failures >= 2:
		return AdmissionDecision{Outcome: AdmissionReject, Reason: ReasonCorroborated}
	case failures == 1:
		return AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonSingleSignal}
	default:
		return AdmissionDecision{Outcome: AdmissionAdmit, Reason: ReasonAdmitted}
	}
}

// CompositionFeatures is one compound candidate's evidence for the split gate: the parts pass and the
// compound pass answers, the abstention and outage flags the fold sets, the store reconstructibility signal,
// and cross-sample disagreement. The fold sets exactly one of PartsDimensional and PartsNotDimensional, and
// exactly one compound status, or the deferred and unavailable flags; a feature the caller cannot establish
// stays false, and the switch's fail-safe branch disposes of any combination the rule set does not recognize.
type CompositionFeatures struct {
	PartsDimensional    bool
	PartsNotDimensional bool
	PartsDeferred       bool
	CompoundNamedUnit   bool
	CompoundComposition bool
	CompoundNoDimension bool
	CompoundDeferred    bool
	JudgeUnavailable    bool
	Reconstructible     bool
	SampleDisagreement  bool
}

// CompositionDecision is the pinned rule set's disposition for one compound: keep it whole, split it
// into its parts, or drop it; the named reason the composition row records; and whether the outcome
// holds the compound from automatic promotion until review.
type CompositionDecision struct {
	Disposition CompositionDisposition
	// Reason is one of this package's CompositionReason* constants; it is empty for the split disposition,
	// whose verdict source (judge, cached, conjunction) belongs to the call and is stamped by the caller
	// through the existing splitSource.
	Reason string
	// Hold reports whether the outcome withholds the compound from automatic promotion until review; it is
	// derived from Reason through compositionHolds, the single definition of the hold policy the
	// persisted-reason lookup also reads.
	Hold bool
}

// DecideComposition disposes of one compound candidate from its features under the pinned rule set of
// docs/tags/definition-and-faceted-retrieval.md §3.6/§8.2/§8.10 and docs/tags/atomicity.md §3.3–§3.5. No
// single signal decides, and evaluation order is load-bearing: a fresh-sample disagreement defers first;
// an unavailable judge keeps the compound undecided (the persisted reason holds it from promotion until a
// later verdict); an abstention defers under split_defer; then the parts test selects the branch — all
// parts dimensional means the combination branch, a failing part means the unit branch — and the compound
// reading disposes within it. In the combination branch a composition reading splits, a no-dimension
// reading conflicts to review, and a named_unit reading is a held gray keep with its parts applied
// alongside (reconstructible when every part is a promoted store value, relational_keep otherwise). In the
// unit branch a named_unit reading keeps whole and a composition reading conflicts to review. An
// unrecognized combination falls to the fail-safe split_defer hold.
func DecideComposition(f CompositionFeatures) CompositionDecision {
	var decision CompositionDecision
	switch {
	case f.SampleDisagreement:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSampleDisagreement}
	case f.JudgeUnavailable:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonJudgeUnavailable}
	case f.CompoundDeferred || f.PartsDeferred:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSplitDefer}
	case f.PartsDimensional && f.CompoundComposition:
		decision = CompositionDecision{Disposition: CompositionSplit}
	case f.PartsDimensional && f.CompoundNoDimension:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonConflictingEvidence}
	case f.PartsDimensional && f.CompoundNamedUnit && f.Reconstructible:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonNamedUnitReconstructible}
	case f.PartsDimensional && f.CompoundNamedUnit:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonRelationalKeep}
	case f.PartsNotDimensional && f.CompoundNamedUnit:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonNamedUnit}
	case f.PartsNotDimensional && f.CompoundComposition:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonConflictingEvidence}
	case f.PartsNotDimensional && f.CompoundNoDimension:
		decision = CompositionDecision{Disposition: CompositionDrop, Reason: CompositionReasonNoDimension}
	default:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSplitDefer}
	}
	decision.Hold = compositionHolds(decision.Reason)
	return decision
}

// DecisionRuleSetVersion pins the admission and promotion rules; the promote log lines carry it so rule
// changes are traceable. Nothing stores it: both rules are pure and re-run over stored criteria. v2 adds
// not_form_genre to the feature set the pinned rules read; v3 rides the criteria-v3 generation — the
// feature set and the pinned combination rules are unchanged, and the bump keeps the promote log's stamp
// rule_set=decision-%s traceable to the judging generation. v4 rides the criteria-v4 generation the same
// way. v5 rides the parts-v3/quality-v5 generation (the parts pass owning the dimension question and the
// language guard), the feature set and the pinned combination rules unchanged. v6 rides the quality-v6
// generation: the not_person_place_work feature becomes not_person (the place and work components leave the
// rubric), the combination rules unchanged.
const DecisionRuleSetVersion = "v6"

// RecurrenceFloor is the promotion minimum of
// docs/tags/definition-and-faceted-retrieval.md §4.3 (P2), in live
// documents: ten until its calibration exists (operator decision D8,
// 2026-09-30). Same-batch mass counts — ten documents from one batch
// remain a systematic proposal, where two are a compression artifact.
const RecurrenceFloor = 10

// DecidePromotion reports whether an admitted candidate accrues enough
// live recurrence to leave provisional standing. Admission below the
// floor is a pending candidate; a deferred or rejected candidate never
// promotes here — the review queue arbitrates those through the human
// action.
func DecidePromotion(outcome AdmissionOutcome, liveMass int64) bool {
	return outcome == AdmissionAdmit && liveMass >= RecurrenceFloor
}
