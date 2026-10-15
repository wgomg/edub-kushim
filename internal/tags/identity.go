package tags

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aaaton/golem/v4"
	"github.com/aaaton/golem/v4/dicts/en"
	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/english"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/textnorm"
)

// IdentityRuleSetVersion pins the birth-time identity rules and the judging body's criteria text; bump
// it when either changes. v1 added the plural rule to the fold-gate criteria; v2 added the derivational
// adjective proposal to the mechanical proposal body; v3 adds the morphographic proposal to the same
// body and the store-anchored rejection routing to the birth rules, the criteria text unchanged; v4
// rewords the pair judge's doubt clause so morphographic and derivation pairs can answer same, the mechanical
// rules unchanged; v5 hardens the split-seam fold — suffix-local morphological anchors over the promoted
// store view — while the birth-time rules and the judging body's criteria are unchanged; v6 adds the
// deterministic variant-class bypass at the unresolved pair loops — the pinned orthographic spelling
// table and literal token permutation resolve mechanically — while the judging body's criteria and the
// house-form map are unchanged; v7 replaces the split seam's shape-based legs (the pinned derivational
// transforms and the suffix-local morphographic distance) with the generated attested pertainym table
// (attested.go) — the seam folds only attested derivations, while the judged identity path still
// proposes shapes and the fold gate confirms them; v8 refines the split seam (a part with more than one
// attested base folds only when exactly one base is a live store value, and a part that did not fold
// through the attestation table may fold onto a pinned deterministic variant anchor) and adds the
// house-form/language step to the birth-time canonical direction before the M4 tie-break. The store reset
// truncates tag_identity_event, so no cached identity verdict survives to invalidate. v9 replaces the seam's
// exactly-one-live-base rule with the primary-sense rule (a multi-base part folds only onto its primary base,
// the residue routed to the phrase-context judged path) and consults the pinned variant classes at birth, so
// a store-anchored orthographic or permutation twin resolves mechanically without a judge. v10 drops the
// similarity context from the pair-adjudication prompt — the two terms are the gate's only evidence — so
// the judged criteria are pinned under the new stamp.
const IdentityRuleSetVersion = "v10"

// The IdentityKind* values name the evidence rows the identity machinery
// records: base-form proposals, the fold gate, the conventional-form
// check, and same-intent shape flags.
const (
	IdentityKindBaseForm         = "base_form"
	IdentityKindFoldGate         = "fold_gate"
	IdentityKindConventionalForm = "conventional_form"
	IdentityKindSameIntent       = "same_intent"
)

// The verdict values an identity evidence row carries.
const (
	IdentityVerdictFold      = "fold"
	IdentityVerdictStand     = "stand"
	IdentityVerdictConfirmed = "confirmed"
	IdentityVerdictRejected  = "rejected"
	IdentityVerdictStands    = "stands"
	IdentityVerdictDefer     = "defer"
)

// Disposition is what the deterministic plan says to do with one
// candidate name.
type Disposition string

// The dispositions a registration can carry: resolve to an existing
// canonical, register a new tag (provisional when flagged), conflict on a
// house-form key match, alias onto a resolved canonical, defer to the
// judging body, or reject as invalid.
const (
	DispositionApply    Disposition = "apply"
	DispositionRegister Disposition = "register"
	DispositionConflict Disposition = "conflict"
	DispositionAlias    Disposition = "alias"
	DispositionDefer    Disposition = "defer"
	DispositionInvalid  Disposition = "invalid"
)

// AliasAction records one alias to write: the alias name, its canonical
// target, and whether the alias's own tag row should leave the store once
// the alias exists.
type AliasAction struct {
	AliasName         string
	Canonical         string
	RemoveExistingTag bool
}

// AliasOutcome is what one planned alias action did: whether it applied at all and how many documents
// it re-pointed; a skipped action — a blocked name, an existing alias, or a missing row — reports Applied
// false with zero documents.
type AliasOutcome struct {
	Applied    bool
	Reassigned int64
}

// Registration is the plan's outcome for one candidate name: the raw
// input, its normalized and house-formed form, the disposition, whether
// it registers provisionally, and the fold the gate should judge.
type Registration struct {
	Input       string
	Name        string
	Resolved    string
	Disposition Disposition
	Provisional bool
	Fold        string
}

// EvidenceRow is one pending identity judgment: its kind, the candidate
// and partner names, the criteria the judging body receives (JSON), the
// verdict once judged, and the model that judged.
type EvidenceRow struct {
	Kind      string
	Candidate string
	Partner   string
	Criteria  json.RawMessage
	Verdict   string
	Model     string
}

// CompositionDisposition is what the composition machinery decides to do with one compound: split it
// into its parts, keep it whole as a dimension, or drop it. It is the composition counterpart of the
// AdmissionOutcome, so the pinned rule set and the persisted rows share a unified typed vocabulary.
type CompositionDisposition string

// The three composition dispositions. The zero value matches none of them, so an unset disposition is
// never silently read as keep or drop.
const (
	CompositionSplit CompositionDisposition = "split"
	CompositionKeep  CompositionDisposition = "keep"
	CompositionDrop  CompositionDisposition = "drop"
)

// The reasons a composition row carries: its verdict source — a fresh judge call, a cached verdict, the
// conjunction grammar fallback, or components that failed validation — the absence of any judge verdict
// (judge_unavailable), the degrade reason when a split's part could not survive admission (part_rejected,
// written by the enricher), and the pinned decision reasons — a named-unit keep, its reconstructible gray
// class, the combination-branch relational keep, a no-dimension drop, a deferred split, parts-versus-compound
// disagreement, and cross-sample disagreement.
const (
	CompositionReasonJudge                    = "judge"
	CompositionReasonCached                   = "cached"
	CompositionReasonConjunction              = "conjunction"
	CompositionReasonInvalidComponents        = "invalid_components"
	CompositionReasonJudgeUnavailable         = "judge_unavailable"
	CompositionReasonPartRejected             = "part_rejected"
	CompositionReasonNamedUnit                = "named_unit"
	CompositionReasonNamedUnitReconstructible = "named_unit_reconstructible"
	CompositionReasonRelationalKeep           = "relational_keep"
	CompositionReasonNoDimension              = "no_dimension"
	CompositionReasonSplitDefer               = "split_defer"
	CompositionReasonConflictingEvidence      = "conflicting_evidence"
	CompositionReasonSampleDisagreement       = "sample_disagreement"
	CompositionReasonPartialParts             = "partial_parts"
)

// compositionHolds is the single definition of which composition reasons withhold a compound from promotion
// until review or a criteria-version bump (docs/tags/atomicity.md §3.5): the cross-sample, conflict, and
// deferred outcomes, the reconstructible and relational named-unit keeps, a degraded keep no judge answered,
// and a decided drop, which mass must not resurrect.
func compositionHolds(reason string) bool {
	switch reason {
	case CompositionReasonSplitDefer, CompositionReasonConflictingEvidence,
		CompositionReasonSampleDisagreement, CompositionReasonNamedUnitReconstructible,
		CompositionReasonRelationalKeep, CompositionReasonJudgeUnavailable,
		CompositionReasonNoDimension, CompositionReasonPartRejected:
		return true
	}
	return false
}

// CompositionRow is one decomposition verdict over a judged compound: the document it belongs to, the
// candidate, the validated components for split rows and the applied-alongside parts for keep rows (empty
// otherwise), the disposition, the verdict source or decision reason, the split criteria version the
// verdict was read under, and the split judge's model for fresh verdicts. Reconstructible records the
// store signal the decision read when the parts pass ran (all parts dimensional and every resolved part
// key holding live mass); it is nil when no parts verdict or store reading exists.
type CompositionRow struct {
	DocumentID      int64
	Candidate       string
	Parts           []string
	Disposition     CompositionDisposition
	Reason          string
	CriteriaVersion string
	Model           string
	Reconstructible *bool
}

// Plan is the deterministic identity plan for a batch of candidate
// names: registrations, alias writes, the evidence rows the judging
// body must settle before anything is written, and the decomposition
// verdicts the split gate recorded for its judged compounds (the
// enricher stamps each row's document id and attaches them; the
// planners leave them empty).
type Plan struct {
	Registrations      []Registration
	Aliases            []AliasAction
	Evidence           []EvidenceRow
	Composition        []CompositionRow
	SkipBlocklistCheck bool
}

// RemovedStoreNames lists the names whose own tag rows leave the store
// because their alias actions say so.
func (p Plan) RemovedStoreNames() []string {
	var out []string
	for _, action := range p.Aliases {
		if action.RemoveExistingTag {
			out = append(out, action.AliasName)
		}
	}
	return out
}

// DropTagNames returns the plan without the registrations and pending
// evidence rows of the given names, so a candidate the admission decision
// rejected is never written; a registration matches by its output name or
// by its raw input, since a deterministic plan's output is house-formed
// and the caller holds emission names. Evidence naming a rejected partner
// goes too, or a later fold could rename a kept candidate onto it.
func (p Plan) DropTagNames(rejected map[string]bool) Plan {
	if len(rejected) == 0 {
		return p
	}
	registrations := make([]Registration, 0, len(p.Registrations))
	for _, reg := range p.Registrations {
		if rejected[reg.Name] || rejected[reg.Input] {
			continue
		}
		registrations = append(registrations, reg)
	}
	evidence := make([]EvidenceRow, 0, len(p.Evidence))
	for _, row := range p.Evidence {
		if rejected[row.Candidate] || rejected[row.Partner] {
			continue
		}
		evidence = append(evidence, row)
	}
	p.Registrations = registrations
	p.Evidence = evidence
	return p
}

// StoreView is the store state a plan is computed against: the live
// names, the aliases that resolve them, and the blocklist.
type StoreView struct {
	Names   []string
	Aliases map[string]string
	Blocked map[string]struct{}
}

var ordinalWordDigits = map[string]string{
	"first": "1st", "second": "2nd", "third": "3rd", "fourth": "4th",
	"fifth": "5th", "sixth": "6th", "seventh": "7th", "eighth": "8th",
	"ninth": "9th", "tenth": "10th", "eleventh": "11th", "twelfth": "12th",
	"thirteenth": "13th", "fourteenth": "14th", "fifteenth": "15th",
	"sixteenth": "16th", "seventeenth": "17th", "eighteenth": "18th",
	"nineteenth": "19th", "twentieth": "20th", "thirtieth": "30th",
	"fortieth": "40th", "fiftieth": "50th", "sixtieth": "60th",
	"seventieth": "70th", "eightieth": "80th", "ninetieth": "90th",
	"hundredth": "100th",
}

var lemmatizer = newEnglishLemmatizer()

func newEnglishLemmatizer() *golem.Lemmatizer {
	l, err := golem.New(en.New())
	if err != nil {
		return nil
	}
	return l
}

// HouseForm applies the house form to a name
// (docs/tags/canonical-form-and-identity.md §3.2 M1): ordinal words become
// digits and Roman numerals become digits. Style rewrites are written
// directly, never proposed.
func HouseForm(name string) string {
	tokens := strings.Fields(name)
	changed := false
	for i, token := range tokens {
		converted := houseFormToken(token)
		if converted != token {
			tokens[i] = converted
			changed = true
		}
	}
	if !changed {
		return name
	}
	return strings.Join(tokens, " ")
}

func houseFormToken(token string) string {
	if digit, ok := ordinalWordDigits[token]; ok {
		return digit
	}
	if isEnglishWord(token) {
		return token
	}
	if n, ok := parseRoman(token); ok {
		return strconv.Itoa(n)
	}
	return token
}

var romanDigits = map[byte]int{
	'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000,
}

var romanRenders = []struct {
	value int
	text  string
}{
	{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"},
	{100, "c"}, {90, "xc"}, {50, "l"}, {40, "xl"},
	{10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"},
}

func parseRoman(s string) (int, bool) {
	if len(s) < 2 {
		return 0, false
	}
	total := 0
	prev := 0
	for i := len(s) - 1; i >= 0; i-- {
		value, ok := romanDigits[s[i]]
		if !ok {
			return 0, false
		}
		if value < prev {
			total -= value
		} else {
			total += value
			prev = value
		}
	}
	if total < 1 || total > 3999 {
		return 0, false
	}
	if renderRoman(total) != s {
		return 0, false
	}
	return total, true
}

func renderRoman(n int) string {
	var sb strings.Builder
	for _, r := range romanRenders {
		for n >= r.value {
			sb.WriteString(r.text)
			n -= r.value
		}
	}
	return sb.String()
}

// Key is the house-form key equality
// (docs/tags/canonical-form-and-identity.md §3.2 M3): style variants
// collapse onto one key without judging.
func Key(name string) string {
	return HouseForm(name)
}

// StoredName returns the name the store persists for a raw candidate: the house form over the normalized
// name. It is the single recipe for the store's name namespace, so the identity plan, the tag table,
// and the composition journal key a name identically.
func StoredName(raw string) string {
	return HouseForm(textnorm.NormalizeTag(raw))
}

// VariantClass reports the deterministic variant class relating two store-normalized names: "orthographic"
// when the pinned spelling table maps both to one form while the originals differ, and "inverted" when
// they hold the same token multiset in a different order; an empty result means neither class holds. It
// never consults the store, and callers pass names already normalized at their write boundaries. Plurals,
// mixed-language spellings, and morphological or function-word inversions (history of anarchism beside
// anarchist history) stay judged, as does any pair the transform table does not reach (D23).
func VariantClass(a, b string) string {
	if a == b {
		return ""
	}
	at, bt := strings.Fields(a), strings.Fields(b)
	if sameTokenMultiset(at, bt) {
		return "inverted"
	}
	if orthographicForm(a) == orthographicForm(b) {
		return "orthographic"
	}
	return ""
}

// orthographicSuffixes is the pinned spelling-variant table VariantClass applies per token, each pair
// mapping a non-canonical token onto its canonical form; a token the table does not reach stays unchanged.
// The -our/-or pair (labour→labor, colour→color, neighbour→neighbor) is store-anchored at the seam, so a
// collision the table could mispair (four/for, tour/tor) folds only when the anchor form is a promoted
// store value.
var orthographicSuffixes = []struct{ suffix, canonical string }{
	{"isation", "ization"},
	{"yse", "yze"},
	{"ogue", "og"},
	{"our", "or"},
}

func orthographicForm(name string) string {
	tokens := strings.Fields(name)
	for i, token := range tokens {
		for _, t := range orthographicSuffixes {
			if before, ok := strings.CutSuffix(token, t.suffix); ok {
				tokens[i] = before + t.canonical
				break
			}
		}
	}
	return strings.Join(tokens, " ")
}

func sameTokenMultiset(a, b []string) bool {
	as := slices.Clone(a)
	bs := slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

// VariantKey returns a normalized name's two pinned variant signatures: its orthographic canonical form and
// its sorted token multiset. Two distinct names are a pinned variant pair (VariantClass != "") exactly when
// they share one of these keys, so a caller comparing one name against a large name set can index the set's
// keys once and probe in O(1) instead of running the per-pair transform for every comparison.
func VariantKey(name string) (orthographic, multiset string) {
	tokens := strings.Fields(name)
	slices.Sort(tokens)
	return orthographicForm(name), strings.Join(tokens, "\x00")
}

// Signature is a name's lemma and stem signatures
// (docs/tags/canonical-form-and-identity.md §3.2 M2). They are evidence
// only; the stemmer's output is never written to the store.
type Signature struct {
	Lemmas string
	Stems  string
}

// ComputeSignature computes a name's lemma and stem signatures.
func ComputeSignature(name string) Signature {
	tokens := strings.Fields(name)
	lemmas := make([]string, len(tokens))
	stems := make([]string, len(tokens))
	for i, token := range tokens {
		lemmas[i] = lemmaOf(token)
		stems[i] = stemOf(token)
	}
	return Signature{
		Lemmas: strings.Join(lemmas, " "),
		Stems:  strings.Join(stems, " "),
	}
}

// ShapeRelatedTo reports whether the two signatures agree on lemmas or
// on stems.
func (s Signature) ShapeRelatedTo(other Signature) bool {
	return s.Lemmas == other.Lemmas || s.Stems == other.Stems
}

// NameIndex caches the keys and shape signatures of a set of names so
// shape comparisons do not recompute them.
type NameIndex struct {
	keys  map[string]string
	shape map[string]Signature
}

// NewNameIndex indexes the given names.
func NewNameIndex(names []string) *NameIndex {
	ix := &NameIndex{
		keys:  make(map[string]string, len(names)),
		shape: make(map[string]Signature, len(names)),
	}
	for _, name := range names {
		ix.ensure(name)
	}
	return ix
}

func (ix *NameIndex) ensure(name string) {
	if _, ok := ix.shape[name]; ok {
		return
	}
	ix.keys[name] = Key(name)
	ix.shape[name] = ComputeSignature(name)
}

// ShapeRelated reports whether two names are shape related: their keys
// differ (key equality is handled earlier) but their lemmas or stems
// agree. Empty and equal names are never related.
func (ix *NameIndex) ShapeRelated(a, b string) bool {
	if a == "" || b == "" || a == b {
		return false
	}
	ix.ensure(a)
	ix.ensure(b)
	if ix.keys[a] == ix.keys[b] {
		return false
	}
	return ix.shape[a].ShapeRelatedTo(ix.shape[b])
}

// distinctNames normalizes each name with normalize and returns the distinct non-empty results in
// first-seen order, the shared input boundary of the name lookups: textnorm.NormalizeTag for the alias and
// held-name reads, StoredName for the reads keyed by the store's namespace (the composition journal). An
// empty or all-empty input yields an empty slice.
func distinctNames(names []string, normalize func(string) string) []string {
	distinct := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		n := normalize(name)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		distinct = append(distinct, n)
	}
	return distinct
}

// ResolveAliases maps each normalized name to its canonical form through
// the alias table; names without an alias are absent from the result.
func ResolveAliases(ctx context.Context, q *database.Queries, names []string) (map[string]string, error) {
	normalized := distinctNames(names, textnorm.NormalizeTag)
	if len(normalized) == 0 {
		return map[string]string{}, nil
	}

	rows, err := q.ResolveTagAliasNames(ctx, normalized)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.AliasName] = row.CanonicalName
	}
	return out, nil
}

// IdentityHeldNames maps each held candidate name to the live store value
// its refused fold targeted. A name is held when, under the current rule
// set, the latest fold_gate verdict rejected the fold or the latest
// base_form verdict leaves the name standing, and the proposal's target is
// a live tag — either refusal blocks the fold. Unheld names are absent
// from the result; empty input returns an empty map.
func IdentityHeldNames(ctx context.Context, q *database.Queries, names []string) (map[string]string, error) {
	normalized := distinctNames(names, textnorm.NormalizeTag)
	if len(normalized) == 0 {
		return map[string]string{}, nil
	}

	rows, err := q.ListIdentityHeldNames(ctx, database.ListIdentityHeldNamesParams{
		RuleSetVersion: IdentityRuleSetVersion,
		Candidates:     normalized,
	})
	if err != nil {
		return nil, err
	}
	held := make(map[string]string)
	for _, row := range rows {
		if row.Anchor == "" {
			continue
		}
		refused := row.Kind == IdentityKindFoldGate && row.Verdict.String == IdentityVerdictRejected ||
			row.Kind == IdentityKindBaseForm && row.Verdict.String == IdentityVerdictStand
		if !refused {
			continue
		}
		held[row.Candidate] = row.Anchor
	}
	return held, nil
}

// CompositionReview reads each named candidate's latest composition journal row under criteriaVersion once
// and returns both views the stored-compound sweep consults: the hold reasons keyed like
// CompositionHeldNames and the states keyed like CompositionStatesForNames. A caller needing both views pays
// one journal read instead of two.
func CompositionReview(ctx context.Context, q *database.Queries, names []string, criteriaVersion string) (map[string]string, map[string]CompositionState, error) {
	normalized := distinctNames(names, StoredName)
	if len(normalized) == 0 {
		return map[string]string{}, map[string]CompositionState{}, nil
	}

	rows, err := q.LatestCompositionReasonsForNames(ctx, database.LatestCompositionReasonsForNamesParams{
		CriteriaVersion: criteriaVersion,
		Candidates:      normalized,
	})
	if err != nil {
		return nil, nil, err
	}
	held := make(map[string]string, len(rows))
	states := make(map[string]CompositionState, len(rows))
	for _, row := range rows {
		if compositionHolds(row.Reason) {
			held[row.Candidate] = row.Reason
		}
		states[row.Candidate] = CompositionState{
			Disposition: CompositionDisposition(row.Disposition),
			Decided:     row.Reason != CompositionReasonJudgeUnavailable,
		}
	}
	return held, states, nil
}

// CompositionHeldNames maps each held compound candidate to the reason its latest composition verdict
// withheld promotion: a deferred split, conflicts between the parts and compound passes, cross-sample
// disagreement, the reconstructible named-unit gray class, the combination-branch relational keep, or a
// degraded keep whose judge never answered (no promotion without a decision; docs/tags/atomicity.md
// §3.5). Only rows carrying criteriaVersion count, so a criteria-version bump clears stale holds. Names
// are normalized to the store's name recipe (StoredName), matching the journal's keys and the sibling
// state read. It is one view of CompositionReview. Unheld candidates are absent from the result; empty
// input returns an empty map.
func CompositionHeldNames(ctx context.Context, q *database.Queries, names []string, criteriaVersion string) (map[string]string, error) {
	held, _, err := CompositionReview(ctx, q, names, criteriaVersion)
	return held, err
}

// CompositionState is one compound candidate's latest persisted composition state under a criteria
// version: the disposition and whether the row is a decision at all. Decided is false exactly for a
// degraded keep (judge_unavailable) — the compound pass never answered, so the row is evidence that
// judgment is still owed, not a verdict to replay.
type CompositionState struct {
	Disposition CompositionDisposition
	Decided     bool
}

// CompositionStatesForNames returns each named candidate's latest composition state under
// criteriaVersion: it is the version-filtered read the store-existing skip consults, so a compound is
// skipped as an application only when a decision actually exists, and a compound carrying no row or only
// a degraded keep stays a candidate for judgment. Names are normalized to the store's name recipe
// (StoredName), matching the journal's keys; it is one view of CompositionReview, and candidates without a
// row are absent from the result (empty input returns an empty map).
func CompositionStatesForNames(ctx context.Context, q *database.Queries, names []string, criteriaVersion string) (map[string]CompositionState, error) {
	_, states, err := CompositionReview(ctx, q, names, criteriaVersion)
	return states, err
}

// ProposedFold proposes the lemma-folded form of a name, or "" when the
// name is already in base form. In-dictionary tokens fold through the
// lemmatizer; a token the dictionary does not know falls back to a
// conservative plural strip, so plurals the dictionary lacks still reach
// the fold gate. A proposed fold is never written unconfirmed; the
// judging body's gate decides.
func ProposedFold(name string) string {
	tokens := strings.Fields(name)
	changed := false
	for i, token := range tokens {
		lemma := lemmaOf(token)
		// unchanged means either a base form or a word outside the
		// dictionary; only the unknown word takes the plural fallback,
		// or known base forms ending in s would propose junk strips
		if lemma == token && !isEnglishWord(token) {
			lemma = pluralStrip(token)
		}
		if lemma != token {
			tokens[i] = lemma
			changed = true
		}
	}
	if !changed {
		return ""
	}
	return strings.Join(tokens, " ")
}

// pluralStrip applies the conservative plural fallback to one token the
// lemmatizer leaves unchanged and does not know (ProposedFold's fallback):
// all lowercase ASCII letters, length ≥ 5, -ies becomes -y and a final -s
// that is not -ss, -us, or -is is dropped. Anything else stands.
func pluralStrip(token string) string {
	if len(token) < 5 || !isLowerASCII(token) {
		return token
	}
	if strings.HasSuffix(token, "ies") {
		return token[:len(token)-3] + "y"
	}
	if strings.HasSuffix(token, "s") && !strings.HasSuffix(token, "ss") &&
		!strings.HasSuffix(token, "us") && !strings.HasSuffix(token, "is") {
		return token[:len(token)-1]
	}
	return token
}

// StoreNames builds the Key(name)-to-name view of a store's names, the
// first name winning when two names share a key. Equality resolution and
// derivational proposals read the same view, so both land on the same
// store row.
func StoreNames(names []string) map[string]string {
	byKey := make(map[string]string, len(names))
	for _, name := range names {
		key := Key(name)
		if _, ok := byKey[key]; !ok {
			byKey[key] = name
		}
	}
	return byKey
}

// StoreNamesOf builds the Key(name)-to-name view of a set of names; it is
// StoreNames for callers that hold the store's names as a set.
func StoreNamesOf(names map[string]struct{}) map[string]string {
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	return StoreNames(list)
}

// ProposedDerivationalFold proposes the noun base of an adjective through
// the derivational suffix transforms, or "" when no transform output is a
// store value. The proposal is store-anchored: a base only exists when the
// store already holds it, so a confirmed fold always resolves onto that
// row, and two distinct store hits are ambiguity, not a fold. Irregular
// demonyms outside the transform set stay; the judging body's gate decides
// before anything is applied (docs/tags/atomicity.md §3.1,
// docs/tags/canonical-form-and-identity.md §3.4). storeNames maps
// Key(storeName) to the store's name.
func ProposedDerivationalFold(name string, storeNames map[string]string) string {
	if len(strings.Fields(name)) != 1 || len(name) < 6 || !isLowerASCII(name) {
		return ""
	}
	return storeAnchor(derivationalBases(name), storeNames)
}

// storeAnchor resolves candidate bases onto a store view (Key(storeName)→storeName): it returns the store
// name the single base the store holds maps to, or "" when no base hits or two distinct bases hit — two
// hits are ambiguity, not a fold. The attested seam fold and the judged path's derivational proposal share
// this anchoring rule, so both stay store-anchored and ambiguity-safe by construction.
func storeAnchor(bases []string, storeNames map[string]string) string {
	hit := ""
	for _, base := range bases {
		storeName, ok := storeNames[Key(base)]
		if !ok {
			continue
		}
		if hit != "" && storeName != hit {
			return ""
		}
		hit = storeName
	}
	return hit
}

// derivationalBases returns the candidate base forms one suffix pass over
// a name produces; the store decides which of them is real.
func derivationalBases(name string) []string {
	var bases []string
	for _, t := range derivationalSuffixes {
		if before, ok := strings.CutSuffix(name, t.suffix); ok {
			bases = append(bases, before+t.replacement)
		}
	}
	return bases
}

// derivationalSuffixes are the adjective→noun transforms the proposal
// applies, pinned to the observed classes: -an and -n strip
// (chilean→chile, american→america) and -al either strips or takes a final
// s (political→politic, political→politics), whichever the store holds.
var derivationalSuffixes = []struct{ suffix, replacement string }{
	{"an", ""},
	{"n", ""},
	{"al", ""},
	{"al", "s"},
}

// morphographicMaxDistance is the edit-distance bound the morphographic
// proposal allows between a candidate and a single-token store key.
const morphographicMaxDistance = 2

// ProposedMorphologicalFold proposes a single-token store value reachable
// from an unrecognized single-token name by at most morphographicMaxDistance
// edits (mexican→mexico), or "" when none is, when two distinct store
// values are in reach (ambiguity is not a fold), or when the name is not
// one lowercase-ASCII token of at least six letters. Store-anchored by
// construction, exactly like the derivational proposer: a confirmed fold
// always resolves onto an existing row, and only single-token store keys
// participate. The judging body's gate decides before anything is applied
// (docs/tags/atomicity.md §3.1). storeNames maps Key(storeName) to the
// store's name.
func ProposedMorphologicalFold(name string, storeNames map[string]string) string {
	if len(strings.Fields(name)) != 1 || len(name) < 6 || !isLowerASCII(name) {
		return ""
	}
	hit := ""
	for key := range storeNames {
		if len(strings.Fields(key)) != 1 || boundedEditDistance(name, key) > morphographicMaxDistance {
			continue
		}
		if hit != "" && storeNames[key] != hit {
			return ""
		}
		hit = storeNames[key]
	}
	return hit
}

// AnchoredFold resolves a split component through the promoted store view. A single-base part folds onto its
// attested base through the shared storeAnchor rule. A part with more than one attested base is
// sense-ambiguous: it folds mechanically only onto its primary-sense base (the attestation table's first
// entry, which preserves WordNet sense order) and only when that base is live — a live non-primary sibling
// sense no longer attracts the fold, and the residue (primary base not live, another base live) is left to
// the phrase-context judged path (ResidualFoldAnchors). A part the attestation table does not
// reach still folds when a promoted store label is a pinned deterministic variant of it (M8 orthographic
// spelling or literal token permutation), resolved by variantAnchor under the same ambiguity-safe rule. The
// attestation is the point: a pertainym relator links an adjective and a noun that denote the same thing, so
// the seam folds only attested relations or pinned variant classes and never infers a fold from bare shape
// (docs/tags/canonical-form-and-identity.md V2/J1/M8). Shape-based proposals (ProposedDerivationalFold,
// ProposedMorphologicalFold) still reach the judged identity path's fold gate, which confirms them before
// use; split-component resolution consults this before per-document filtering, and births never rename
// through it. promotedNames maps Key(storeName) to the promoted store's name; liveNames is the live store
// key set the primary-base liveness check reads, which the caller must supply. provisional births are absent
// from promotedNames and so anchor only through the judged identity path.
func AnchoredFold(name string, promotedNames map[string]string, liveNames map[string]struct{}) string {
	bases := attestedBases(name)
	if len(bases) == 0 {
		return variantAnchor(name, promotedNames)
	}
	if len(bases) > 1 {
		if _, live := liveNames[Key(bases[0])]; !live {
			return ""
		}
	}
	if anchor := storeAnchor(bases[:1], promotedNames); anchor != "" {
		return anchor
	}
	return variantAnchor(name, promotedNames)
}

// ResidualFoldAnchors returns the live attested bases of a multi-base part whose primary-sense base is not
// live: the phrase-context fold call's candidate anchors. It returns nil when the part is not
// multi-base, when its primary base is live (the seam folds mechanically), or when no other base is live
// (the part falls to the judged identity path). liveNames is the live store key set.
func ResidualFoldAnchors(name string, liveNames map[string]struct{}) []string {
	bases := attestedBases(name)
	if len(bases) < 2 {
		return nil
	}
	if _, live := liveNames[Key(bases[0])]; live {
		return nil
	}
	var anchors []string
	for _, base := range bases[1:] {
		if _, live := liveNames[Key(base)]; live {
			anchors = append(anchors, base)
		}
	}
	return anchors
}

// PromotedKeys returns the Key set of a Key→name store view, the promoted guard the verdict store reads.
func PromotedKeys(view map[string]string) map[string]struct{} {
	set := make(map[string]struct{}, len(view))
	for key := range view {
		set[key] = struct{}{}
	}
	return set
}

// PromotedGuardKeys returns the Key set of a raw store name set, so a caller holding names rather than a
// Key→name view builds the same promoted guard through one rule.
func PromotedGuardKeys(names map[string]struct{}) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for name := range names {
		set[Key(name)] = struct{}{}
	}
	return set
}

// variantAnchor resolves a name onto a promoted store label related by a pinned deterministic variant
// class (M8), or "" when none is or when two distinct labels are in reach — ambiguity is not a fold.
// Unlike the attested legs it takes the name's pinned variant classes rather than a lexical relation,
// so an orthographic spelling pair (labour/labor) or a literal token permutation folds store-anchored.
func variantAnchor(name string, promotedNames map[string]string) string {
	want := tokenCount(name)
	hit := ""
	for _, storeName := range promotedNames {
		if storeName == name || tokenCount(storeName) != want {
			continue
		}
		if VariantClass(name, storeName) == "" {
			continue
		}
		if hit != "" && storeName != hit {
			return ""
		}
		hit = storeName
	}
	return hit
}

// tokenCount counts whitespace-separated tokens without allocating, so the variant scan skips store labels
// of a different arity cheaply before running the variant transforms.
func tokenCount(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			n++
		}
	}
	return n
}

// PreferCanonical picks the canonical label of a same-intent pair the judged direction did not separate
// (docs/tags/canonical-form-and-identity.md §3.3 J4): a pinned deterministic variant class still resolves
// through M4, as M8 specifies (the class carries no direction), then the English/house form wins over a
// non-English one, and only a pair the language step cannot separate falls to the deterministic tie-break
// (M4). It is the birth-time direction step the alias machinery reads; arrival order and usage never
// decide.
func PreferCanonical(a, b string) string {
	if a == b {
		return a
	}
	if VariantClass(a, b) != "" {
		return TieBreak(a, b)
	}
	aEnglish, bEnglish := isEnglishName(a), isEnglishName(b)
	if aEnglish != bEnglish {
		if aEnglish {
			return a
		}
		return b
	}
	return TieBreak(a, b)
}

// CanonicalLoser resolves a same-intent pair's canonical label (winner) and its alias (loser) under J4: the
// judged direction names the winner directly, and a tie — including every deterministic resolution, which
// carries no direction — falls to PreferCanonical. Every alias-application site reads this one rule, so a
// change to J4 is made once.
func CanonicalLoser(direction, query, target string) (winner, loser string) {
	switch direction {
	case analyzer.TagDirectionTarget:
		return target, query
	case analyzer.TagDirectionQuery:
		return query, target
	}
	if PreferCanonical(query, target) == query {
		return query, target
	}
	return target, query
}

// tokenEnglish reports whether one token is English for the language signal: a numeric ordinal is
// language-neutral and counts, a non-ASCII token is never English, and any other token is English when the
// dictionary knows it. Both the direction step and the D27 guard read this one predicate, so they cannot
// disagree about a token.
func tokenEnglish(tok string) bool {
	if isNumericToken(tok) {
		return true
	}
	if hasNonASCII(tok) {
		return false
	}
	return isEnglishWord(tok)
}

// isEnglishName reports whether every token of a name is English per tokenEnglish; an empty name is not
// English. It is the language signal the birth-time direction step reads.
func isEnglishName(name string) bool {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return false
	}
	for _, tok := range fields {
		if !tokenEnglish(tok) {
			return false
		}
	}
	return true
}

// DetectNonEnglish reports whether a name has any token that is not English per tokenEnglish; it is the
// complement of isEnglishName over non-empty names. It is a candidate signal, not a verdict — the judged
// translation pass confirms it and returns English jargon unchanged.
func DetectNonEnglish(name string) bool {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return false
	}
	for _, tok := range fields {
		if !tokenEnglish(tok) {
			return true
		}
	}
	return false
}

func hasNonASCII(tok string) bool {
	for _, r := range tok {
		if r > 127 {
			return true
		}
	}
	return false
}

// splitNumericToken splits a leading digit run from its trailing suffix, the one place the numeric-token
// shape is parsed.
func splitNumericToken(tok string) (digits int, suffix string) {
	for digits < len(tok) && tok[digits] >= '0' && tok[digits] <= '9' {
		digits++
	}
	return digits, tok[digits:]
}

// isNumericToken reports whether a token is a run of digits optionally carrying an English ordinal
// suffix (19th, 3rd), which the English-word check must not treat as foreign.
func isNumericToken(tok string) bool {
	digits, suffix := splitNumericToken(tok)
	if digits == 0 {
		return false
	}
	return suffix == "" || isOrdinalSuffix(suffix)
}

// isOrdinalSuffix reports whether a digit run's trailing letters are an English ordinal suffix, the one
// place the suffix set lives.
func isOrdinalSuffix(suffix string) bool {
	switch suffix {
	case "st", "nd", "rd", "th":
		return true
	}
	return false
}

// AllPartsPromoted reports whether every part names a promoted store label: the established-value proxy the composition decision's
// reconstructibility signal reads. Promotion implies a live extension, so a promoted label is already past the recurrence floor, while a
// provisional part below it is not treated as a store intersection. promotedNames maps Key(storeName) to the promoted store's name; an empty
// part list reports true.
func AllPartsPromoted(parts []string, promotedNames map[string]string) bool {
	for _, part := range parts {
		if _, ok := promotedNames[Key(part)]; !ok {
			return false
		}
	}
	return true
}

// boundedEditDistance returns the Levenshtein distance between a and b,
// capped at morphographicMaxDistance: any larger distance reads as the
// bound plus one, which no caller can mistake for a hit. The length and
// row bounds prune most store candidates before the matrix fills.
func boundedEditDistance(a, b string) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(a)-len(b) > morphographicMaxDistance {
		return morphographicMaxDistance + 1
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if cur[j] < best {
				best = cur[j]
			}
		}
		if best > morphographicMaxDistance {
			return morphographicMaxDistance + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func isLowerASCII(token string) bool {
	for i := 0; i < len(token); i++ {
		if token[i] < 'a' || token[i] > 'z' {
			return false
		}
	}
	return true
}

// TieBreak is the deterministic tie-break for labels the direction
// criteria do not separate
// (docs/tags/canonical-form-and-identity.md §3.2 M4): the
// lexicographically smaller name wins.
func TieBreak(a, b string) string {
	if a <= b {
		return a
	}
	return b
}

func lemmaOf(token string) string {
	if lemmatizer == nil {
		return token
	}
	return lemmatizer.Lemma(token)
}

func isEnglishWord(token string) bool {
	if lemmatizer == nil {
		return false
	}
	return lemmatizer.InDict(token)
}

func stemOf(token string) string {
	env := snowballstem.NewEnv(token)
	english.Stem(env)
	return env.Current()
}

// PlanDeterministic plans the disposition of candidate names against the
// store view. It never proposes a fold, adjudicates, or writes an alias:
// flagged shape relations register provisionally and leave pending
// evidence rows for the judging body.
func PlanDeterministic(names []string, view StoreView) Plan {
	byKey := StoreNames(view.Names)

	ix := NewNameIndex(view.Names)
	plan := Plan{}
	for _, raw := range names {
		name := StoredName(raw)
		reg := Registration{Input: raw, Name: name, Disposition: DispositionRegister}
		if name == "" {
			reg.Disposition = DispositionInvalid
			plan.Registrations = append(plan.Registrations, reg)
			continue
		}
		if _, blocked := view.Blocked[name]; blocked {
			reg.Disposition = DispositionInvalid
			plan.Registrations = append(plan.Registrations, reg)
			continue
		}
		if canonical, ok := view.Aliases[name]; ok {
			reg.Disposition = DispositionApply
			reg.Resolved = canonical
			plan.Registrations = append(plan.Registrations, reg)
			continue
		}
		if existing, ok := byKey[Key(name)]; ok {
			if existing == name {
				reg.Disposition = DispositionConflict
			} else {
				reg.Disposition = DispositionApply
			}
			reg.Resolved = existing
			plan.Registrations = append(plan.Registrations, reg)
			continue
		}
		flagged := false
		for _, existing := range view.Names {
			if !ix.ShapeRelated(name, existing) {
				continue
			}
			flagged = true
			plan.Evidence = append(plan.Evidence, EvidenceRow{
				Kind:      IdentityKindSameIntent,
				Candidate: name,
				Partner:   existing,
			})
		}
		fold := ProposedFold(name)
		if fold == "" || fold == name {
			fold = ProposedDerivationalFold(name, byKey)
		}
		if fold == "" {
			fold = ProposedMorphologicalFold(name, byKey)
		}
		if fold != "" {
			flagged = true
			reg.Fold = fold
			plan.Evidence = append(plan.Evidence,
				EvidenceRow{Kind: IdentityKindBaseForm, Candidate: name},
				EvidenceRow{Kind: IdentityKindFoldGate, Candidate: name},
				EvidenceRow{Kind: IdentityKindConventionalForm, Candidate: name},
			)
		}
		if flagged {
			reg.Provisional = true
		}
		plan.Registrations = append(plan.Registrations, reg)
	}
	return plan
}
