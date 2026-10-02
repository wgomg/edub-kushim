package tags

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aaaton/golem/v4"
	"github.com/aaaton/golem/v4/dicts/en"
	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/english"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/textnorm"
)

// IdentityRuleSetVersion pins the birth-time identity rules and the
// judging body's criteria text; bump it when either changes. v1 added the
// plural rule to the fold-gate criteria; v2 added the derivational
// adjective proposal to the mechanical proposal body; v3 adds the
// morphographic proposal to the same body and the store-anchored
// rejection routing to the birth rules, the criteria text unchanged. The
// store reset truncates tag_identity_event, so no cached verdict survives
// to invalidate.
const IdentityRuleSetVersion = "v3"

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

// The dispositions a composition row can carry: the compound split into
// its parts, kept whole as a dimension, or dropped because an admitted
// part was rejected downstream.
const (
	CompositionSplit = "split"
	CompositionKeep  = "keep"
	CompositionDrop  = "drop"
)

// The reasons a composition row carries: its verdict source — a fresh
// judge call, a cached verdict, the conjunction grammar fallback, or
// components that failed validation — the absence of a judge
// (judge_unavailable), and the degrade reason when a split's part could
// not survive admission (part_rejected, written by the enricher).
const (
	CompositionReasonJudge             = "judge"
	CompositionReasonCached            = "cached"
	CompositionReasonConjunction       = "conjunction"
	CompositionReasonInvalidComponents = "invalid_components"
	CompositionReasonJudgeUnavailable  = "judge_unavailable"
	CompositionReasonPartRejected      = "part_rejected"
)

// CompositionRow is one decomposition verdict over a judged compound: the
// document it belongs to, the candidate, the validated parts for split
// rows (empty otherwise), the disposition, the verdict source or degrade
// reason, and the split judge's model for fresh verdicts. CriteriaVersion
// pins the split criteria the verdict was read under.
type CompositionRow struct {
	DocumentID      int64
	Candidate       string
	Parts           []string
	Disposition     string
	Reason          string
	CriteriaVersion string
	Model           string
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

// ResolveAliases maps each normalized name to its canonical form through
// the alias table; names without an alias are absent from the result.
func ResolveAliases(ctx context.Context, q *database.Queries, names []string) (map[string]string, error) {
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		n := textnorm.NormalizeTag(name)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
	}
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
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		n := textnorm.NormalizeTag(name)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
	}
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
	hit := ""
	for _, base := range derivationalBases(name) {
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
		name := HouseForm(textnorm.NormalizeTag(raw))
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
