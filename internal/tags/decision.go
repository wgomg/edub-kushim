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
	SubjectMatter      bool
	Dimension          bool
	NotPersonPlaceWork bool
	Atomic             bool
	NotFormGenre       bool
	JudgeDefer         bool
	CategoryOther      bool
	NoAnchor           bool
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
		!f.NotPersonPlaceWork,
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

// DecisionRuleSetVersion pins the admission and promotion rules; the
// promote log lines carry it so rule changes are traceable. Nothing
// stores it: both rules are pure and re-run over stored criteria. v2
// adds not_form_genre to the feature set the pinned rules read.
const DecisionRuleSetVersion = "v2"

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
