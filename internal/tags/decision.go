package tags

import (
	"time"

	"github.com/wgomg/edub-kushim/internal/types"
)

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

// AdmissionFeatures is the evidence the admission decision reads for one candidate: the judge's rubric
// answers plus its abstention, the parts pass's stamped dimension verdict, and the deterministic signals
// the emission context supplies. A boolean feature the caller cannot establish stays false and never counts
// as failing — absence of evidence is not evidence; an empty PersonClass instead defers to review, never
// licensing a rejection.
type AdmissionFeatures struct {
	SubjectMatter bool
	Dimension     bool
	PersonClass   string
	Atomic        bool
	NotFormGenre  bool
	JudgeDefer    bool
	CategoryOther bool
	NoAnchor      bool
}

// The reasons an admission decision carries into the logs: a judge abstention, a candidate judged neither
// subject matter nor a dimension (the definition's drop leg, §3.6), a documented individual's name (the
// people facet), a non-real or undecided person reading held for review, two or more failing signals, a
// failure set of deterministic signals alone, one lone failing signal in the gray band, no failing signal,
// or an operator who waived a review deferral.
const (
	ReasonJudgeDefer        = "judge_defer"
	ReasonNoDimension       = "no_dimension"
	ReasonPersonFacet       = "person_facet"
	ReasonPersonReview      = "person_review"
	ReasonCorroborated      = "corroborated_failures"
	ReasonDeterministicOnly = "deterministic_only"
	ReasonSingleSignal      = "single_signal"
	ReasonAdmitted          = "admitted"
	ReasonOperatorAccept    = "operator_accept"
)

// The triggers that cost a value its standing (docs/specs/tags/definition.md §6.4, §8): mass decay below
// the recurrence floor, or an operator. A verdict-driven demotion records the admission decision's own
// reason (one of this package's Reason* constants) and a decided drop the composition reason it was
// decided under, so a stored reason always names the evidence without a second vocabulary here.
const (
	DemotionMassDecay = "mass_decay"
	DemotionOperator  = "operator"
)

// AdmissionDecision carries the disposition and the named reason that
// drove it: one of this package's Reason* constants, or an interim path's
// own reason.
type AdmissionDecision struct {
	Outcome AdmissionOutcome
	Reason  string
}

// DecideAdmission decides one candidate from its features: evidence, not verdicts — no single signal
// decides, with the one class-structured exception the person gateway pins. A judge abstention defers
// first, then a candidate judged neither subject matter nor a dimension is the definition's drop leg and
// rejects as no_dimension. The person class decides next: a documented individual rejects as person_facet
// independent of subject_matter (a biography is about its subject while the name belongs to the people
// facet), while the mythological, legendary, fictional, undecided, and not-established classes defer as
// person_review — only a tag naming no person reaches the corroborated ladder. There two or more failing
// signals reject only when at least one is judge-owned (corroborated evidence); a failure set of the
// deterministic signals alone defers as deterministic_only; exactly one failing signal defers to review
// (the gray band refuses single-signal drops); and none admits. The rule set is pinned and versioned — the
// quality version pins the judge features behind it — and runs with zero labels.
func DecideAdmission(f AdmissionFeatures) AdmissionDecision {
	if f.JudgeDefer {
		return AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonJudgeDefer}
	}
	if !f.SubjectMatter && !f.Dimension {
		return AdmissionDecision{Outcome: AdmissionReject, Reason: ReasonNoDimension}
	}
	switch f.PersonClass {
	case types.PersonClassRealIndividual:
		return AdmissionDecision{Outcome: AdmissionReject, Reason: ReasonPersonFacet}
	case types.PersonClassNotPerson:
		// the only class that continues to the corroborated ladder
	default:
		return AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonPersonReview}
	}
	judgeFailures := 0
	for _, failed := range []bool{
		!f.SubjectMatter,
		!f.Dimension,
		!f.Atomic,
		!f.NotFormGenre,
	} {
		if failed {
			judgeFailures++
		}
	}
	detFailures := 0
	for _, failed := range []bool{f.CategoryOther, f.NoAnchor} {
		if failed {
			detFailures++
		}
	}
	total := judgeFailures + detFailures
	switch {
	case total >= 2 && judgeFailures >= 1:
		return AdmissionDecision{Outcome: AdmissionReject, Reason: ReasonCorroborated}
	case detFailures >= 2:
		return AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonDeterministicOnly}
	case total == 1:
		return AdmissionDecision{Outcome: AdmissionDefer, Reason: ReasonSingleSignal}
	default:
		return AdmissionDecision{Outcome: AdmissionAdmit, Reason: ReasonAdmitted}
	}
}

// CompositionFeatures is one compound candidate's evidence for the split gate. The fold sets exactly one of
// PartsDimensional and PartsNotDimensional and exactly one compound status — or the deferred and unavailable
// flags — and a feature the fold cannot establish stays false, so an unrecognized combination reaches the
// switch's fail-safe branch rather than a wrong disposition.
type CompositionFeatures struct {
	PartsDimensional    bool
	PartsNotDimensional bool
	PartsDeferred       bool
	CompoundNamedUnit   bool
	CompoundComposition bool
	CompoundNoDimension bool
	CompoundDeferred    bool
	JudgeUnavailable    bool
	// Reconstructible marks that the parts reconstruct the compound: the substrate reading's firm or gray
	// band when one is sufficient, the promoted-parts proxy when none exists.
	Reconstructible bool
	// FarFromIntersection marks the substrate reading's far band: the compound selects more (or less) than
	// its parts' intersection, so it is not their product.
	FarFromIntersection bool
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
// docs/specs/tags/definition.md §3.6/§8 and docs/specs/tags/atomicity.md §3.3–§3.5. No single signal
// decides, and evaluation order is load-bearing: a fresh-sample disagreement defers first;
// an unavailable judge keeps the compound undecided (the persisted reason holds it from promotion until a
// later verdict); an abstention defers under split_defer; then the parts test selects the branch — all
// parts dimensional means the combination branch, a failing part means the unit branch — and the compound
// reading disposes within it. In the combination branch a composition reading splits, unless a sufficient
// reconstruction reading lands far from the intersection: the whole is then not its parts' product and the
// compound holds as relational_keep for review (atomicity §3.3 step B's far leg). A no-dimension reading
// conflicts to review, and a named_unit reading keeps with its parts applied alongside; the reason recorded
// is named_unit_reconstructible, a final keep whose standing the recurrence floor and mass decay decide,
// when the reading is firm or gray (or, with no reading established, every part is a promoted store
// value), and relational_keep otherwise. A relational keep is held for review on a substrate reading;
// one resting on the promoted-parts proxy without promoted parts is store-maturity pending and unheld
// until the sweep re-disposes it. In the unit branch a named_unit reading keeps whole and a composition
// reading conflicts to review. An unrecognized combination falls to the fail-safe split_defer hold.
func DecideComposition(f CompositionFeatures) CompositionDecision {
	var decision CompositionDecision
	switch {
	case f.SampleDisagreement:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSampleDisagreement}
	case f.JudgeUnavailable:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonJudgeUnavailable}
	case f.CompoundDeferred || f.PartsDeferred:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonSplitDefer}
	case f.PartsDimensional && f.CompoundComposition && f.FarFromIntersection:
		decision = CompositionDecision{Disposition: CompositionKeep, Reason: CompositionReasonRelationalKeep}
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

// DecisionRuleSetVersion pins the admission, promotion, and demotion rules; the promote and demote log
// lines carry it so rule changes are traceable. Nothing stores it: every rule is pure and re-runs over
// stored criteria. v2 adds not_form_genre to the feature set the pinned rules read, and v3 rides the
// criteria-v3 generation — the feature set and the pinned combination rules are unchanged, and the bump
// keeps the promote log's stamp rule_set=decision-%s traceable to the judging generation. v4 rides the
// criteria-v4 generation the same way. v5 rides the parts-v3/quality-v5 generation, the parts pass owning
// the dimension question and the language guard, the feature set and the pinned combination rules stay
// unchanged. v6 rides the quality-v6 generation: the not_person_place_work feature becomes not_person,
// the place and work components leaving the rubric, the combination rules unchanged. v7 adds the demotion
// rules to the set: a decayed live mass below the configured recurrence floor, a decided no-dimension drop,
// and a fresh reject or defer under the current versions revoke influence, the admission rules unchanged.
// v8 adds the retention rules to the set: a current-version decided drop or reject standing past the
// retention window removes the stored row, the admission and demotion rules unchanged. v9 structures
// the admission rules: a candidate judged neither subject matter nor a dimension rejects with its own
// no_dimension reason, and a rejection requires at least one judge-owned failing signal — a failure set of
// deterministic signals alone defers as deterministic_only — the demotion and retention rules unchanged.
// v10 adds the operator acceptance: a review deferral waived by an editor admits the value to the ordinary
// promotion paths, the admission, demotion and retention rules unchanged. v11 releases the reconstructible
// named-unit composition keep from the promotion hold: the compound is a final keep that promotes at the
// recurrence floor and demotes on mass decay like any provisional candidate, the admission, demotion and
// retention rules unchanged. v12 supersedes D53's facet-veto rejection for the person question: the
// not_person boolean leaves the feature set for the five-class person_class answer, a documented individual
// rejects as person_facet independent of subject_matter, the non-real and undecided classes (and an absent
// class) defer as person_review before the corroborated ladder, and only a tag naming no person continues
// to it — the demotion and retention rules unchanged. v13 adds the operator-precedence rules: a sweep
// records fresh verdicts as evidence and notifies, but never revokes a promotion an operator granted
// (standing_source 'operator') and never re-opens an acceptance; retention never removes an accepted row;
// and an operator separation is terminal for its (candidate, anchor) pair across rule-set versions.
const DecisionRuleSetVersion = "v13"

// DecidePromotion reports whether an admitted candidate at or above the configured floor promotes.
func DecidePromotion(outcome AdmissionOutcome, liveMass int64, floor int64) bool {
	return outcome == AdmissionAdmit && liveMass >= floor
}

// VerdictDemotionReasons is the closed set of triggers a verdict-driven demotion records: the admission
// decision's own reasons plus the composition reasons a decided drop demotes under. A demotion stamped with
// one of these is a rule-driven revocation — the restore action's scope — as opposed to a mass-decay or an
// operator demotion, which no rule re-derives.
var VerdictDemotionReasons = []string{
	ReasonJudgeDefer,
	ReasonNoDimension,
	ReasonPersonFacet,
	ReasonPersonReview,
	ReasonCorroborated,
	ReasonDeterministicOnly,
	ReasonSingleSignal,
	CompositionReasonPartRejected,
}

// Standing provenance recorded on a promoted tag row. Operator standing is a human decision and outlives
// criteria-version re-judgments: a sweep records the fresh verdict as evidence and notifies, never demotes.
// Automatic standing answers to the ordinary demotion rules, mass decay included.
const (
	StandingAuto     = "auto"
	StandingOperator = "operator"
)

// Promotion reasons recorded in the audit log. Producers and the provenance mapping share these names, so
// a new promotion path cannot silently fall back to automatic standing by inventing a spelling.
const (
	PromoteReasonHumanApprove    = "human-approve"
	PromoteReasonSweep           = "sweep"
	PromoteReasonSweepAccepted   = "sweep-accepted"
	PromoteReasonRejudge         = "rejudge"
	PromoteReasonRejudgeAccepted = "rejudge-accepted"
)

// StandingSourceForPromoteReason maps a promotion's audit reason to the provenance the row records. Human
// approval and the acceptance-governed paths are operator decisions; every automatic path stays auto and
// answers to the ordinary demotion rules.
func StandingSourceForPromoteReason(reason string) string {
	switch reason {
	case PromoteReasonHumanApprove, PromoteReasonSweepAccepted, PromoteReasonRejudgeAccepted:
		return StandingOperator
	default:
		return StandingAuto
	}
}

// WithAcceptance resolves one derived decision against an operator acceptance: while an acceptance is
// stamped on a provisional value, any defer — cached or freshly judged — admits it to the ordinary promotion
// floor, so a criteria-version bump or a re-judge never re-opens the deferral the operator waived. A
// rejection is never waived (the operator rejects to remove the value), and a missing acceptance still
// keeps the derived decision. The acceptance is consumed by promotion and revoked only by an operator
// action, never by a newer verdict.
func WithAcceptance(derived AdmissionDecision, acceptedAt time.Time) AdmissionDecision {
	if acceptedAt.IsZero() || derived.Outcome != AdmissionDefer {
		return derived
	}
	return AdmissionDecision{Outcome: AdmissionAdmit, Reason: ReasonOperatorAccept}
}

// DecideMassDemotion reports whether live mass below the configured recurrence floor drops standing.
func DecideMassDemotion(liveMass int64, floor int64) bool {
	return liveMass < floor
}

// RetentionFeatures is the evidence the retention rule reads for one stored row: whether the current
// criteria version has decided it names no dimension or failed admission, whether review holds it, whether
// the orphan sweep owns it because its live extension is empty, and whether the decision has stood past
// the retention window. The decision features default false, so absence of a decision never removes;
// IdentityHeld is the caller's hold read and must be established before the rule runs.
type RetentionFeatures struct {
	DecidedDrop   bool
	RejectStamped bool
	IdentityHeld  bool
	LiveMassZero  bool
	DecisionOld   bool
}

// DecideRetention reports whether a stored row may be removed: only a current-version decided drop or
// admission reject whose decision has stood past the retention window, with no identity hold and live mass
// above zero. A row with an empty live extension belongs to the orphan sweep, and a held or undecided row
// stays for review — absence of evidence never removes (docs/specs/tags/definition.md §6.4).
func DecideRetention(f RetentionFeatures) bool {
	return (f.DecidedDrop || f.RejectStamped) && !f.IdentityHeld && !f.LiveMassZero && f.DecisionOld
}
