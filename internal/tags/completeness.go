package tags

// CoverageClass is the class a carried (or missing) dimension falls in for coverage accounting
// (docs/tags/completeness.md §3.4). The classes are counted separately so retrieval-readiness is never
// overstated: a pending class means the dimension will be carried once a standing operation runs, while a
// missing one is a gap.
type CoverageClass string

// The coverage classes: a canonical label attached (aliases resolved), a still-whole compound whose
// decomposition will produce the dimension, a stored label that denotes the dimension but has not been
// swept, a dimension with no value, a gray judgment deferred to review, the skip path's durable
// pending-coverage class, and the negative verdict recorded as a calibration label (not a coverage gap).
const (
	CoverageExplicit                CoverageClass = "explicit"
	CoveragePendingSplit            CoverageClass = "pending_split"
	CoveragePendingCanonicalization CoverageClass = "pending_canonicalization"
	CoverageMissing                 CoverageClass = "missing"
	CoverageGray                    CoverageClass = "gray"
	CoveragePendingCoverage         CoverageClass = "pending_coverage"
	CoverageNotApplies              CoverageClass = "not_applies"
)

// CoverageSource names the instrument that proposed a dimension: the matcher trigger (strong band), the
// lexical band that survived narrowing, or the emission skip path.
type CoverageSource string

// The proposal sources.
const (
	CoverageSourceTrigger CoverageSource = "trigger"
	CoverageSourceLexical CoverageSource = "lexical"
	CoverageSourceSkip    CoverageSource = "skip"
)

// CoverageAction is what the audit did with a coverage reading: attached the dimension, flagged it to
// review, recorded it with no action, or recorded a proposal for a value that does not exist yet.
type CoverageAction string

// The coverage actions.
const (
	CoverageActionFill     CoverageAction = "fill"
	CoverageActionFlag     CoverageAction = "flag"
	CoverageActionNone     CoverageAction = "none"
	CoverageActionProposal CoverageAction = "proposal"
)

// CoverageRecord is one (document, dimension) coverage reading: the class, the proposing instrument, the
// audit verdict and its model when judged, the action taken, whether the carried value is provisional, and
// the versions the reading was computed under. Verdict is the persisted criteria JSON (nil for an
// unjudged, derived reading).
type CoverageRecord struct {
	DocumentID      int64
	Dimension       string
	Class           CoverageClass
	Source          CoverageSource
	Verdict         []byte
	VerdictModel    string
	TextHash        string
	CriteriaVersion string
	Action          CoverageAction
	Provisional     bool
	RuleSetVersion  string
}

// ResolvedContent returns a compound's content words and their position-aligned deterministic resolved
// forms: a word the live view does not hold as a time designation is emptied, a word the attested seam fold
// anchors carries its anchor, and every other word is itself. This is the shared seam the emission gate and
// coverage accounting both read, so the fold decision lives here once. liveNames is the live store key set;
// promotedNames is the promoted store view the fold anchors on.
func ResolvedContent(name string, promotedNames map[string]string, liveNames map[string]struct{}) (content, singles []string) {
	content = DeriveParts(name)
	singles = make([]string, len(content))
	for i, word := range content {
		if IsTimeDesignationDrop(word, liveNames) {
			continue
		}
		singles[i] = word
		if anchor := AnchoredFold(word, promotedNames, liveNames); anchor != "" && anchor != word {
			singles[i] = anchor
		}
	}
	return content, singles
}

// ResolvedBigrams returns the contiguous bigrams of content whose two words both resolved and which are not a
// time designation the store lacks. The attested fold is single-token, so a bigram is literal: its resolved
// form is itself.
func ResolvedBigrams(content, singles []string, liveNames map[string]struct{}) []string {
	var out []string
	for i := 0; i+1 < len(content); i++ {
		if i+1 >= len(singles) || singles[i] == "" || singles[i+1] == "" {
			continue
		}
		bigram := content[i] + " " + content[i+1]
		if IsTimeDesignationDrop(bigram, liveNames) {
			continue
		}
		out = append(out, bigram)
	}
	return out
}

// ResolvedSegments returns a compound's decomposition segments in their applied store form: its resolved
// content words plus every accepted bigram, resolved through the shared fold seam (ResolvedContent and
// ResolvedBigrams). A seeded period does not decompose, so its carrier resolves to nothing. Coverage
// accounting reads this deterministic resolution; the gate may additionally apply judged phrase-context
// folds, so a coverage reading is a lower bound on what the gate will emit
// (docs/tags/atomicity.md §3.1).
func ResolvedSegments(name string, promotedNames map[string]string, liveNames map[string]struct{}) []string {
	if IsPeriodClass(HouseForm(name)) {
		// a seeded period does not decompose: the gate keeps it whole and applies no parts, so a period
		// carrier covers no dimension as a pending split
		return nil
	}
	content, singles := ResolvedContent(name, promotedNames, liveNames)
	out := make([]string, 0, len(content)*2)
	for _, resolved := range singles {
		if resolved != "" {
			out = append(out, resolved)
		}
	}
	return append(out, ResolvedBigrams(content, singles, liveNames)...)
}

// CoversDimension reports whether a still-whole compound's decomposition segments include one that denotes
// the dimension — the pending-split coverage reading: the compound will produce the dimension when the
// standing decomposition or a re-emission materializes its cover (docs/tags/completeness.md §3.4).
func CoversDimension(compound, dimension string, promotedNames map[string]string, liveNames map[string]struct{}) bool {
	key := Key(dimension)
	for _, segment := range ResolvedSegments(compound, promotedNames, liveNames) {
		if Key(segment) == key {
			return true
		}
	}
	return false
}

// PendingCoverageSegments returns a compound's resolved segments that are pending coverage: neither a live
// store value nor carrying a cached parts verdict under the current policy version. It is the durable form
// of the emission skip path's pending-coverage warning (D35), read over composition evidence
// (docs/tags/completeness.md §3.4).
func PendingCoverageSegments(compound string, promotedNames map[string]string, liveNames map[string]struct{}, cached map[string]struct{}) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, segment := range ResolvedSegments(compound, promotedNames, liveNames) {
		if _, live := liveNames[Key(segment)]; live {
			continue
		}
		if _, ok := cached[segment]; ok {
			continue
		}
		if _, ok := seen[segment]; ok {
			continue
		}
		seen[segment] = struct{}{}
		out = append(out, segment)
	}
	return out
}

// TwinPartner reports whether a canonicalization worklist records the two names as one pair; the
// pending-canonicalization reading (docs/tags/completeness.md §3.4) consults it so a carried label that
// denotes the dimension under the canonicalization rules is counted as pending rather than missing.
func TwinPartner(twins []Twin, a, b string) bool {
	for _, t := range twins {
		if (t.A == a && t.B == b) || (t.A == b && t.B == a) {
			return true
		}
	}
	return false
}
