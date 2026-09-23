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
	"github.com/wgomg/edub-kushim/internal/utils"
)

// pins the birth-time identity rules and the judging body's criteria text;
// bump it when either changes
const IdentityRuleSetVersion = "v0"

const (
	IdentityKindBaseForm         = "base_form"
	IdentityKindFoldGate         = "fold_gate"
	IdentityKindConventionalForm = "conventional_form"
	IdentityKindSameIntent       = "same_intent"
)

const (
	IdentityVerdictFold      = "fold"
	IdentityVerdictStand     = "stand"
	IdentityVerdictConfirmed = "confirmed"
	IdentityVerdictRejected  = "rejected"
	IdentityVerdictStands    = "stands"
	IdentityVerdictDefer     = "defer"
)

type Disposition string

const (
	DispositionApply    Disposition = "apply"
	DispositionRegister Disposition = "register"
	DispositionConflict Disposition = "conflict"
	DispositionAlias    Disposition = "alias"
	DispositionDefer    Disposition = "defer"
	DispositionInvalid  Disposition = "invalid"
)

type AliasAction struct {
	AliasName         string
	Canonical         string
	RemoveExistingTag bool
}

type Registration struct {
	Input       string
	Name        string
	Resolved    string
	Disposition Disposition
	Provisional bool
	Fold        string
}

type EvidenceRow struct {
	Kind      string
	Candidate string
	Partner   string
	Criteria  json.RawMessage
	Verdict   string
	Model     string
}

type Plan struct {
	Registrations      []Registration
	Aliases            []AliasAction
	Evidence           []EvidenceRow
	SkipBlocklistCheck bool
}

func (p Plan) RemovedStoreNames() []string {
	var out []string
	for _, action := range p.Aliases {
		if action.RemoveExistingTag {
			out = append(out, action.AliasName)
		}
	}
	return out
}

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

// docs/tags/canonical-form-and-identity.md §3.2 M1: house form; style rewrites
// are written directly
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

// docs/tags/canonical-form-and-identity.md §3.2 M3: key equality, so style
// variants collapse without judging
func Key(name string) string {
	return HouseForm(name)
}

// docs/tags/canonical-form-and-identity.md §3.2 M2: lemma and stem signatures
// are evidence only; the stemmer's output is never written
type Signature struct {
	Lemmas string
	Stems  string
}

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

func (s Signature) ShapeRelatedTo(other Signature) bool {
	return s.Lemmas == other.Lemmas || s.Stems == other.Stems
}

type NameIndex struct {
	keys  map[string]string
	shape map[string]Signature
}

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

func ResolveAliases(ctx context.Context, q *database.Queries, names []string) (map[string]string, error) {
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		n := utils.NormalizeTag(name)
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

// a proposed fold is never written unconfirmed; the judging body's gate
// decides
func ProposedFold(name string) string {
	tokens := strings.Fields(name)
	changed := false
	for i, token := range tokens {
		lemma := lemmaOf(token)
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

// docs/tags/canonical-form-and-identity.md §3.2 M4: deterministic tie-break for
// labels the direction criteria do not separate
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

// never proposes a fold, adjudicates, or makes an alias; flagged shape
// relations register provisionally and leave pending evidence
func PlanDeterministic(names []string, view StoreView) Plan {
	byKey := make(map[string]string, len(view.Names))
	for _, name := range view.Names {
		key := Key(name)
		if _, ok := byKey[key]; !ok {
			byKey[key] = name
		}
	}

	ix := NewNameIndex(view.Names)
	plan := Plan{}
	for _, raw := range names {
		name := HouseForm(utils.NormalizeTag(raw))
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
		if fold := ProposedFold(name); fold != "" && fold != name {
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
