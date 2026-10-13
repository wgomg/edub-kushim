package tags

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
)

func TestHouseForm(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"nineteenth century", "19th century"},
		{"first", "1st"},
		{"world war ii", "world war 2"},
		{"xiv", "14"},
		{"mix design", "mix design"},
		{"history", "history"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HouseForm(tc.name); got != tc.want {
				t.Errorf("HouseForm(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestTieBreak(t *testing.T) {
	if got := TieBreak("b", "a"); got != "a" {
		t.Errorf("TieBreak(b, a) = %q, want a", got)
	}
	if got := TieBreak("a", "b"); got != "a" {
		t.Errorf("TieBreak(a, b) = %q, want a", got)
	}
}

func TestVariantClass(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want string
	}{
		{"pinned -isation/-ization", "decolonisation", "decolonization", "orthographic"},
		{"pinned -yse/-yze", "analyse", "analyze", "orthographic"},
		{"pinned -ogue/-og", "catalogue", "catalog", "orthographic"},
		{"per-token spelling inside a phrase", "history organisation", "history organization", "orthographic"},
		{"literal token permutation", "military history", "history military", "inverted"},
		{"repeated tokens compare as a multiset", "war war history", "history war war", "inverted"},
		{"token counts are not a set", "war war history", "war history history", ""},
		{"equal names are not a variant", "railway", "railway", ""},
		{"plural is not a spelling variant", "railway", "railways", ""},
		{"morphological and function-word inversion stays judged", "history of anarchism", "anarchist history", ""},
		{"inflected spelling form stays judged", "organisations", "organizations", ""},
		{"pinned -our/-or", "labour", "labor", "orthographic"},
		{"spelling plus permutation stays judged", "organisation history", "history organization", ""},
		{"translation is not a spelling variant", "guerra", "war", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VariantClass(tc.a, tc.b); got != tc.want {
				t.Errorf("VariantClass(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
			if got := VariantClass(tc.b, tc.a); got != tc.want {
				t.Errorf("reversed VariantClass(%q, %q) = %q, want %q", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func TestPluralStrip(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"servlets", "servlet"},
		{"stories", "story"},
		{"glass", "glass"},       // -ss endings never strip
		{"status", "status"},     // -us endings never strip
		{"analysis", "analysis"}, // -is endings never strip
		{"news", "news"},         // too short to strip
		{"Abcde", "Abcde"},       // only all-lowercase tokens strip
		{"un_der", "un_der"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pluralStrip(tc.name); got != tc.want {
				t.Errorf("pluralStrip(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestProposedFold_PluralFallback(t *testing.T) {
	// the two controls pin the dictionary membership each case depends on:
	// an unknown plural must exercise the strip, a known word must not
	if isEnglishWord("websockets") {
		t.Fatalf("%q is in the lemmatizer dictionary; pick an out-of-dictionary plural for the strip fallback", "websockets")
	}
	if !isEnglishWord("chaos") {
		t.Fatalf("%q is missing from the lemmatizer dictionary; pick an in-dictionary word for the no-strip guard", "chaos")
	}
	cases := []struct {
		name string
		want string
	}{
		{"servlets", "servlet"},     // the review's out-of-dictionary plural still proposes
		{"websockets", "websocket"}, // unknown plural proposes through the strip fallback
		{"stories", "story"},        // -ies becomes -y
		{"chaos", ""},               // in-dictionary words never fall through to the strip
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProposedFold(tc.name); got != tc.want {
				t.Errorf("ProposedFold(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestProposedDerivationalFold(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		storeNames map[string]string
		want       string
	}{
		{
			name:       "an suffix strips to the store noun",
			input:      "chilean",
			storeNames: map[string]string{"chile": "chile"},
			want:       "chile",
		},
		{
			name:       "final n strips to the store noun",
			input:      "russian",
			storeNames: map[string]string{"russia": "russia"},
			want:       "russia",
		},
		{
			name:       "al suffix proposes the store plural",
			input:      "political",
			storeNames: map[string]string{"politics": "politics"},
			want:       "politics",
		},
		{
			name:       "al suffix proposes the bare store noun",
			input:      "political",
			storeNames: map[string]string{"politic": "politic"},
			want:       "politic",
		},
		{
			name:  "two store hits are ambiguity, not a fold",
			input: "political",
			storeNames: map[string]string{
				"politic":  "politic",
				"politics": "politics",
			},
			want: "",
		},
		{
			name:       "no transform output anchors to the store",
			input:      "political",
			storeNames: map[string]string{"social": "social"},
			want:       "",
		},
		{
			name:       "irregular demonym outside the transform set stays",
			input:      "german",
			storeNames: map[string]string{"germany": "germany"},
			want:       "",
		},
		{
			name:       "multi-token names never propose",
			input:      "chilean people",
			storeNames: map[string]string{"chile": "chile"},
			want:       "",
		},
		{
			name:       "names under the length guard never propose",
			input:      "legal",
			storeNames: map[string]string{"leg": "leg"},
			want:       "",
		},
		{
			name:       "uppercase names never propose",
			input:      "Chilean",
			storeNames: map[string]string{"chile": "chile"},
			want:       "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProposedDerivationalFold(tc.input, tc.storeNames); got != tc.want {
				t.Errorf("ProposedDerivationalFold(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestProposedMorphologicalFold(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		storeNames map[string]string
		want       string
	}{
		{
			name:       "distance one proposes the store value",
			input:      "spudgers",
			storeNames: map[string]string{"spudger": "spudger"},
			want:       "spudger",
		},
		{
			name:       "distance two reaches the store noun",
			input:      "mexican",
			storeNames: map[string]string{"mexico": "mexico"},
			want:       "mexico",
		},
		{
			name:       "distance three is out of reach",
			input:      "spudging",
			storeNames: map[string]string{"spudger": "spudger"},
			want:       "",
		},
		{
			name:  "two store hits are ambiguity, not a fold",
			input: "spudgers",
			storeNames: map[string]string{
				"spudger":  "spudger",
				"spudgery": "spudgery",
			},
			want: "",
		},
		{
			name:       "multi-token names never propose",
			input:      "mexican food",
			storeNames: map[string]string{"mexico": "mexico"},
			want:       "",
		},
		{
			name:       "names under the length guard never propose",
			input:      "mexic",
			storeNames: map[string]string{"mexico": "mexico"},
			want:       "",
		},
		{
			name:       "uppercase names never propose",
			input:      "Mexican",
			storeNames: map[string]string{"mexico": "mexico"},
			want:       "",
		},
		{
			name:       "multi-word store keys never participate",
			input:      "spudgers",
			storeNames: map[string]string{"spud ger": "spud ger"},
			want:       "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProposedMorphologicalFold(tc.input, tc.storeNames); got != tc.want {
				t.Errorf("ProposedMorphologicalFold(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestAnchoredFold(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		storeNames map[string]string
		want       string
	}{
		{
			name:       "an attested derivation lands on the store noun",
			input:      "chilean",
			storeNames: map[string]string{"chile": "chile"},
			want:       "chile",
		},
		{
			name:       "an attested relation reaches the store noun",
			input:      "mexican",
			storeNames: map[string]string{"mexico": "mexico"},
			want:       "mexico",
		},
		{
			name:       "a proposal that equals the lemma fold stays judged",
			input:      "railways",
			storeNames: map[string]string{"railway": "railway"},
			want:       "",
		},
		{
			name:  "two store hits are ambiguity, not a fold",
			input: "spudgers",
			storeNames: map[string]string{
				"spudger":  "spudger",
				"spudgery": "spudgery",
			},
			want: "",
		},
		{
			name:       "a store-exact name without an attested derivation does not fold",
			input:      "revolution",
			storeNames: map[string]string{"revolution": "revolution"},
			want:       "",
		},
		{
			name:       "no store anchor resolves nothing",
			input:      "mexican",
			storeNames: map[string]string{"spain": "spain"},
			want:       "",
		},
		{
			name:       "multi-token names never propose",
			input:      "mexican food",
			storeNames: map[string]string{"mexico": "mexico"},
			want:       "",
		},
		{
			name:       "a prefix-divergent near-spelling never folds",
			input:      "oppression",
			storeNames: map[string]string{"repression": "repression"},
			want:       "",
		},
		{
			name:       "an attested relation folds through the store anchor",
			input:      "german",
			storeNames: map[string]string{"germany": "germany"},
			want:       "germany",
		},
		{
			name:       "an attested relation resolves the adjective",
			input:      "political",
			storeNames: map[string]string{"politics": "politics"},
			want:       "politics",
		},
		{
			name:       "an attested base the store lacks does not fold",
			input:      "roman",
			storeNames: map[string]string{"romania": "romania"},
			want:       "",
		},
		{
			name:       "an orthographic variant folds onto the promoted spelling",
			input:      "labour",
			storeNames: map[string]string{"labor": "labor"},
			want:       "labor",
		},
		{
			name:       "a multi-token orthographic variant folds onto the promoted spelling",
			input:      "labour movement",
			storeNames: map[string]string{"labor movement": "labor movement"},
			want:       "labor movement",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnchoredFold(tc.input, tc.storeNames, PromotedKeys(tc.storeNames)); got != tc.want {
				t.Errorf("AnchoredFold(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestAnchoredFold_LiveView pins the seam's primary-sense rule against the live store view the emission
// path passes: a part with more than one attested base folds mechanically only onto its primary-sense base
// (the table's first entry, WordNet sense order) and only while that base is live — a live non-primary
// sibling sense neither attracts nor blocks the fold, and the residue is left to the phrase-context judged
// path. The promoted-only callers collapse to the same rule with the promoted view as the live
// view.
func TestAnchoredFold_LiveView(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		promoted map[string]string
		live     []string
		want     string
	}{
		{
			name:     "a multi-base part folds onto its live primary base",
			input:    "german",
			promoted: map[string]string{"germany": "germany"},
			live:     []string{"germany", "deutschland"},
			want:     "germany",
		},
		{
			name:     "a live non-primary base does not attract the fold",
			input:    "civil",
			promoted: map[string]string{"state": "state"},
			live:     []string{"state"},
			want:     "",
		},
		{
			name:     "the primary base must be live to fold",
			input:    "german",
			promoted: map[string]string{"germany": "germany"},
			live:     []string{"deutschland"},
			want:     "",
		},
		{
			name:     "a variant anchor folds regardless of the sibling check",
			input:    "labour",
			promoted: map[string]string{"labor": "labor"},
			live:     []string{"labor"},
			want:     "labor",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := make(map[string]struct{}, len(tc.live))
			for _, name := range tc.live {
				live[Key(name)] = struct{}{}
			}
			if got := AnchoredFold(tc.input, tc.promoted, live); got != tc.want {
				t.Errorf("AnchoredFold(%q, live=%v) = %q, want %q", tc.input, tc.live, got, tc.want)
			}
		})
	}
}

// TestPreferCanonical pins the birth-time canonical direction (docs/tags/canonical-form-and-identity.md
// J4): an English house form wins over a non-English label in either order, a deterministic variant class
// keeps resolving through the lexicographic tie-break because it carries no direction, and two labels the
// language step cannot separate fall through to the tie-break.
func TestPreferCanonical(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
		want string
	}{
		{"the house form wins over a foreign label", "filosofia", "philosophy", "philosophy"},
		{"order does not change the winner", "philosophy", "filosofia", "philosophy"},
		{"a deterministic variant still resolves through the tie-break", "decolonisation", "decolonization", "decolonisation"},
		{"labels the language step cannot separate fall to the tie-break", "20th century", "19th century", "19th century"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PreferCanonical(tc.a, tc.b); got != tc.want {
				t.Errorf("PreferCanonical(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestVariantKey pins the two signatures the birth-time scan precomputes: a pinned variant pair always
// shares one of them, and two names that share neither are never a pair — so the precomputed probe and the
// per-pair VariantClass transform cannot disagree.
func TestVariantKey(t *testing.T) {
	related := []struct{ a, b string }{
		{"color", "colour"},
		{"analyse", "analyze"},
		{"decolonisation", "decolonization"},
		{"war civil", "civil war"},
	}
	for _, tc := range related {
		if VariantClass(tc.a, tc.b) == "" {
			t.Errorf("VariantClass(%q, %q) = \"\", want a pinned class before the key parity holds", tc.a, tc.b)
		}
		aOrth, aMulti := VariantKey(tc.a)
		bOrth, bMulti := VariantKey(tc.b)
		if aOrth != bOrth && aMulti != bMulti {
			t.Errorf("VariantKey(%q) = (%q, %q) and VariantKey(%q) = (%q, %q), want a shared signature",
				tc.a, aOrth, aMulti, tc.b, bOrth, bMulti)
		}
	}

	unrelated := []struct{ a, b string }{
		{"civil war", "economy"},
		{"colour", "colourless"},
		{"revolution", "evolution"},
		{"20th century", "19th century"},
		{"math", "maths"},
	}
	for _, tc := range unrelated {
		if VariantClass(tc.a, tc.b) != "" {
			t.Errorf("VariantClass(%q, %q) = %q, want \"\" for an unrelated pair", tc.a, tc.b, VariantClass(tc.a, tc.b))
		}
		aOrth, aMulti := VariantKey(tc.a)
		bOrth, bMulti := VariantKey(tc.b)
		if aOrth == bOrth || aMulti == bMulti {
			t.Errorf("VariantKey(%q) = (%q, %q) and VariantKey(%q) = (%q, %q), unrelated names must share no signature",
				tc.a, aOrth, aMulti, tc.b, bOrth, bMulti)
		}
	}
}

func TestStoreNames(t *testing.T) {
	got := StoreNames([]string{"16th century", "sixteenth century", "russia"})

	// "sixteenth century" house-forms onto "16th century"'s key; the first
	// name wins so equality resolution and proposals hit one store row
	if len(got) != 2 {
		t.Fatalf("len(StoreNames) = %d, want 2 (the ordinal variant collapses onto one key)", len(got))
	}
	if got["16th century"] != "16th century" {
		t.Errorf("key %q maps to %q, want the first name with that key", "16th century", got["16th century"])
	}
	if got["russia"] != "russia" {
		t.Errorf("key %q maps to %q, want %q", "russia", got["russia"], "russia")
	}
}

func TestPlanDeterministic(t *testing.T) {
	noEvidence := map[string]int{}

	cases := []struct {
		name       string
		view       StoreView
		input      string
		wantDisp   Disposition
		wantName   string
		wantResolv string
		wantProv   bool
		wantFold   string
		wantKinds  map[string]int
	}{
		{
			name: "fresh name normalizes and registers promoted",
			view: StoreView{},
			// normalization happens at the write boundary
			input:     "Fresh Topic",
			wantDisp:  DispositionRegister,
			wantName:  "fresh topic",
			wantKinds: noEvidence,
		},
		{
			name:       "exact existing name conflicts",
			view:       StoreView{Names: []string{"kept tag"}},
			input:      "kept tag",
			wantDisp:   DispositionConflict,
			wantName:   "kept tag",
			wantResolv: "kept tag",
			wantKinds:  noEvidence,
		},
		{
			name:       "key-equal style variant applies the store value",
			view:       StoreView{Names: []string{"world war ii"}},
			input:      "world war 2",
			wantDisp:   DispositionApply,
			wantName:   "world war 2",
			wantResolv: "world war ii",
			wantKinds:  noEvidence,
		},
		{
			name:       "alias resolves to its canonical value",
			view:       StoreView{Names: []string{"kept tag"}, Aliases: map[string]string{"old name": "kept tag"}},
			input:      "old name",
			wantDisp:   DispositionApply,
			wantName:   "old name",
			wantResolv: "kept tag",
			wantKinds:  noEvidence,
		},
		{
			name:      "blocked name is never registered",
			view:      StoreView{Blocked: map[string]struct{}{"blocked name": {}}},
			input:     "blocked name",
			wantDisp:  DispositionInvalid,
			wantName:  "blocked name",
			wantKinds: noEvidence,
		},
		{
			name:     "inflection flags a fold but never applies it",
			view:     StoreView{},
			input:    "buildings",
			wantDisp: DispositionRegister,
			wantName: "buildings",
			wantKinds: map[string]int{
				IdentityKindBaseForm:         1,
				IdentityKindFoldGate:         1,
				IdentityKindConventionalForm: 1,
			},
			wantProv: true,
			wantFold: "building",
		},
		{
			name:     "shape relation records pending evidence without adjudicating",
			view:     StoreView{Names: []string{"running"}},
			input:    "run",
			wantDisp: DispositionRegister,
			wantName: "run",
			wantKinds: map[string]int{
				IdentityKindSameIntent: 1,
			},
			wantProv: true,
			wantFold: "",
		},
		{
			name:     "derivational base proposes onto the store noun",
			view:     StoreView{Names: []string{"russia"}},
			input:    "russian",
			wantDisp: DispositionRegister,
			wantName: "russian",
			wantKinds: map[string]int{
				IdentityKindBaseForm:         1,
				IdentityKindFoldGate:         1,
				IdentityKindConventionalForm: 1,
			},
			wantProv: true,
			wantFold: "russia",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := PlanDeterministic([]string{tc.input}, tc.view)

			if len(plan.Registrations) != 1 {
				t.Fatalf("registrations = %d, want 1", len(plan.Registrations))
			}
			reg := plan.Registrations[0]
			if reg.Disposition != tc.wantDisp {
				t.Errorf("disposition = %q, want %q", reg.Disposition, tc.wantDisp)
			}
			if reg.Name != tc.wantName {
				t.Errorf("name = %q, want %q", reg.Name, tc.wantName)
			}
			if reg.Resolved != tc.wantResolv {
				t.Errorf("resolved = %q, want %q", reg.Resolved, tc.wantResolv)
			}
			if reg.Provisional != tc.wantProv {
				t.Errorf("provisional = %v, want %v", reg.Provisional, tc.wantProv)
			}
			if reg.Fold != tc.wantFold {
				t.Errorf("fold = %q, want %q", reg.Fold, tc.wantFold)
			}
			if len(plan.Aliases) != 0 {
				t.Errorf("aliases = %v, want none: the deterministic phase never makes aliases", plan.Aliases)
			}

			gotKinds := map[string]int{}
			for _, e := range plan.Evidence {
				gotKinds[e.Kind]++
				if e.Verdict != "" {
					t.Errorf("evidence[%s] carries verdict %q; only judged rows may", e.Kind, e.Verdict)
				}
			}
			if len(gotKinds) != len(tc.wantKinds) {
				t.Errorf("evidence kinds = %v, want %v", gotKinds, tc.wantKinds)
			}
			for kind, want := range tc.wantKinds {
				if gotKinds[kind] != want {
					t.Errorf("evidence[%s] = %d, want %d", kind, gotKinds[kind], want)
				}
			}
		})
	}
}

func TestPlan_DropTagNames(t *testing.T) {
	plan := Plan{
		Registrations: []Registration{
			{Input: "kept topic", Name: "kept topic", Disposition: DispositionRegister},
			{Input: "world war ii", Name: "world war 2", Disposition: DispositionRegister},
			{Input: "resolved loser", Resolved: "kept topic", Disposition: DispositionApply},
			{Input: "dropped name", Name: "dropped name", Disposition: DispositionRegister},
		},
		Evidence: []EvidenceRow{
			{Kind: IdentityKindSameIntent, Candidate: "kept topic", Partner: "dropped partner"},
			{Kind: IdentityKindSameIntent, Candidate: "dropped candidate", Partner: "kept topic"},
			{Kind: IdentityKindSameIntent, Candidate: "kept topic", Partner: "kept topic"},
		},
	}
	rejected := map[string]bool{
		"world war ii":      true,
		"dropped name":      true,
		"dropped partner":   true,
		"dropped candidate": true,
	}

	out := plan.DropTagNames(rejected)

	if len(out.Registrations) != 2 {
		t.Fatalf("registrations = %d, want 2 (input-matched house-form and name-matched rejected dropped)", len(out.Registrations))
	}
	for _, reg := range out.Registrations {
		if reg.Name == "dropped name" || reg.Input == "world war ii" {
			t.Errorf("rejected registration survived: %+v", reg)
		}
	}
	if len(out.Evidence) != 1 {
		t.Fatalf("evidence = %d, want 1", len(out.Evidence))
	}
	if out.Evidence[0].Candidate != "kept topic" || out.Evidence[0].Partner != "kept topic" {
		t.Errorf("kept evidence row = %+v", out.Evidence[0])
	}
	if len(plan.Registrations) != 4 || len(plan.Evidence) != 3 {
		t.Errorf("DropTagNames mutated the receiver: registrations=%d evidence=%d", len(plan.Registrations), len(plan.Evidence))
	}

	unchanged := plan.DropTagNames(nil)
	if len(unchanged.Registrations) != 4 || len(unchanged.Evidence) != 3 {
		t.Errorf("empty rejected set must keep the plan: registrations=%d evidence=%d", len(unchanged.Registrations), len(unchanged.Evidence))
	}
}

func TestIdentityHeldNames(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	q := client.Queries

	database.MustCreateTag(t, q, "zqholdanchor")

	seed := func(kind, candidate, verdict, anchor string) {
		t.Helper()
		if _, err := q.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
			Kind:           kind,
			Candidate:      candidate,
			Criteria:       []byte(`{"proposed_fold":"` + anchor + `"}`),
			Verdict:        sql.NullString{String: verdict, Valid: true},
			RuleSetVersion: IdentityRuleSetVersion,
		}); err != nil {
			t.Fatalf("seed %s row for %q: %v", kind, candidate, err)
		}
	}
	// the gate's refusal alone holds the name
	seed(IdentityKindBaseForm, "political", IdentityVerdictFold, "zqholdanchor")
	seed(IdentityKindFoldGate, "political", IdentityVerdictRejected, "zqholdanchor")
	// base_form=stand blocks despite a confirming gate (the mining class)
	seed(IdentityKindBaseForm, "mining", IdentityVerdictStand, "zqholdanchor")
	seed(IdentityKindFoldGate, "mining", IdentityVerdictConfirmed, "zqholdanchor")
	// a refused proposal onto a dead anchor is not a hold
	seed(IdentityKindFoldGate, "ghosty", IdentityVerdictRejected, "zqghost")
	// a confirmed fold with a standing base form never held
	seed(IdentityKindBaseForm, "folded", IdentityVerdictFold, "zqholdanchor")
	seed(IdentityKindFoldGate, "folded", IdentityVerdictConfirmed, "zqholdanchor")
	// conventional-form rows are out of scope for the hold
	seed(IdentityKindConventionalForm, "formatted", IdentityVerdictDefer, "zqholdanchor")

	held, err := IdentityHeldNames(ctx, q, []string{
		"political", "mining", "ghosty", "folded", "formatted", "unjudged",
	})
	if err != nil {
		t.Fatalf("IdentityHeldNames: %v", err)
	}
	if len(held) != 2 {
		t.Errorf("held = %v, want exactly the two refused names", held)
	}
	if held["political"] != "zqholdanchor" {
		t.Errorf("political anchor = %q, want the live store value", held["political"])
	}
	if held["mining"] != "zqholdanchor" {
		t.Errorf("mining anchor = %q, want base_form=stand to hold despite a confirming gate", held["mining"])
	}

	empty, err := IdentityHeldNames(ctx, q, nil)
	if err != nil {
		t.Fatalf("IdentityHeldNames(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty input = %v, want no holds", empty)
	}
}

func TestCompositionHeldNames(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	q := client.Queries
	docID := database.CreateUniqueDocument(t, q, "composition-hold")

	// rows carry an explicit created_at so "latest" is decided by the test rather than by insert timing,
	// and a criteria version of this test's own keeps them out of every other read
	const version = "hold-test-v1"
	const staleVersion = "hold-test-v0"
	seed := func(candidate, reason, criteria string, at time.Time) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx, `
			INSERT INTO tag_composition_event
			    (document_id, candidate, parts, disposition, reason, criteria_version, created_at)
			VALUES ($1, $2, '[]'::jsonb, 'keep', $3, $4, $5)`,
			docID, candidate, reason, criteria, at); err != nil {
			t.Fatalf("seed composition row for %q: %v", candidate, err)
		}
	}

	now := time.Now()
	// the read takes the latest row under one criteria version, so the seeds cover every reason that can
	// hold, every reason that cannot, a candidate a newer verdict settles, and a hold written under another
	// version that must stay invisible to this read; a degraded keep holds too — no promotion without a
	// decision
	seed("heldsplit", CompositionReasonSplitDefer, version, now)
	seed("heldconflict", CompositionReasonConflictingEvidence, version, now)
	seed("helddisagree", CompositionReasonSampleDisagreement, version, now)
	seed("heldgray", CompositionReasonNamedUnitReconstructible, version, now)
	seed("heldrelational", CompositionReasonRelationalKeep, version, now)
	seed("cleankeep", CompositionReasonNamedUnit, version, now)
	seed("degraded", CompositionReasonJudgeUnavailable, version, now)
	seed("dropped", CompositionReasonNoDimension, version, now)
	seed("settled", CompositionReasonSplitDefer, version, now.Add(-time.Hour))
	seed("settled", CompositionReasonNamedUnit, version, now)
	seed("stale", CompositionReasonSplitDefer, staleVersion, now)

	held, err := CompositionHeldNames(ctx, q, []string{
		"  HeldSplit ", "heldconflict", "helddisagree", "heldgray", "heldrelational",
		"cleankeep", "degraded", "dropped", "settled", "stale", "unjudged",
	}, version)
	if err != nil {
		t.Fatalf("CompositionHeldNames: %v", err)
	}
	want := map[string]string{
		"heldsplit":      CompositionReasonSplitDefer,
		"heldconflict":   CompositionReasonConflictingEvidence,
		"helddisagree":   CompositionReasonSampleDisagreement,
		"heldgray":       CompositionReasonNamedUnitReconstructible,
		"heldrelational": CompositionReasonRelationalKeep,
		"degraded":       CompositionReasonJudgeUnavailable,
	}
	if len(held) != len(want) {
		t.Errorf("held = %v, want exactly %v", held, want)
	}
	for name, reason := range want {
		if held[name] != reason {
			t.Errorf("held[%q] = %q, want %q", name, held[name], reason)
		}
	}
	if _, ok := held["settled"]; ok {
		t.Errorf("settled candidate held = %q, a newer non-held verdict must clear the hold", held["settled"])
	}
	if _, ok := held["stale"]; ok {
		t.Errorf("stale candidate held = %q, a hold under another criteria version must not count", held["stale"])
	}

	empty, err := CompositionHeldNames(ctx, q, nil, version)
	if err != nil {
		t.Fatalf("CompositionHeldNames(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty input = %v, want no holds", empty)
	}
}

// TestCompositionStatesForNames pins the read the store-existing skip consults: it is keyed the way the
// journal writes (StoredName), version-filtered, and reports decided exactly when the latest row is not a
// degraded keep — a compound with no row stays a candidate for judgment, and only current-version rows count.
func TestCompositionStatesForNames(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	q := client.Queries
	docID := database.CreateUniqueDocument(t, q, "composition-state")

	// explicit created_at decides "latest" and the test's own criteria versions keep these rows out of
	// every other read
	const version = "state-test-v1"
	const staleVersion = "state-test-v0"
	seed := func(candidate, disposition, reason, criteria string, at time.Time) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx, `
			INSERT INTO tag_composition_event
			    (document_id, candidate, parts, disposition, reason, criteria_version, created_at)
			VALUES ($1, $2, '[]'::jsonb, $3, $4, $5, $6)`,
			docID, candidate, disposition, reason, criteria, at); err != nil {
			t.Fatalf("seed composition row for %q: %v", candidate, err)
		}
	}

	now := time.Now()
	seed("decidedkeep", string(CompositionKeep), CompositionReasonNamedUnit, version, now)
	seed("decideddrop", string(CompositionDrop), CompositionReasonNoDimension, version, now)
	seed("degraded", string(CompositionKeep), CompositionReasonJudgeUnavailable, version, now)
	seed("settled", string(CompositionKeep), CompositionReasonJudgeUnavailable, version, now.Add(-time.Hour))
	seed("settled", string(CompositionKeep), CompositionReasonNamedUnit, version, now)
	seed("stale", string(CompositionKeep), CompositionReasonNamedUnit, staleVersion, now)
	seed("world war 2", string(CompositionKeep), CompositionReasonNamedUnit, version, now)

	states, err := CompositionStatesForNames(ctx, q, []string{
		"  DecidedKeep ", "decideddrop", "degraded", "settled", "stale", "unjudged", "World War II",
	}, version)
	if err != nil {
		t.Fatalf("CompositionStatesForNames: %v", err)
	}
	want := map[string]CompositionState{
		"decidedkeep": {Disposition: CompositionKeep, Decided: true},
		"decideddrop": {Disposition: CompositionDrop, Decided: true},
		"degraded":    {Disposition: CompositionKeep, Decided: false},
		"settled":     {Disposition: CompositionKeep, Decided: true},
		"world war 2": {Disposition: CompositionKeep, Decided: true},
	}
	if len(states) != len(want) {
		t.Errorf("states = %v, want exactly %v", states, want)
	}
	for name, state := range want {
		if got := states[name]; got != state {
			t.Errorf("states[%q] = {Disposition:%q Decided:%t}, want {Disposition:%q Decided:%t}",
				name, got.Disposition, got.Decided, state.Disposition, state.Decided)
		}
	}
	if _, ok := states["unjudged"]; ok {
		t.Errorf("states[unjudged] = %v, a compound with no row must stay a candidate", states["unjudged"])
	}

	empty, err := CompositionStatesForNames(ctx, q, nil, version)
	if err != nil {
		t.Fatalf("CompositionStatesForNames(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty input = %v, want no states", empty)
	}
}

// TestCanonicalLoser pins the shared J4 resolution every alias-application site reads: a judged direction
// names the winner directly, and a tie — including a deterministic variant, which carries no direction —
// falls to PreferCanonical rather than to a site-local copy of the rule.
func TestCanonicalLoser(t *testing.T) {
	cases := []struct {
		name            string
		direction, a, b string
		wantWinner      string
		wantLoser       string
	}{
		{
			name:      "the judged direction names the target the winner",
			direction: analyzer.TagDirectionTarget, a: "philosophy", b: "filosofia",
			wantWinner: "filosofia", wantLoser: "philosophy",
		},
		{
			name:      "the judged direction names the query the winner",
			direction: analyzer.TagDirectionQuery, a: "philosophy", b: "filosofia",
			wantWinner: "philosophy", wantLoser: "filosofia",
		},
		{
			name: "a direction the direction step did not separate falls to the house form",
			a:    "filosofia", b: "philosophy",
			wantWinner: "philosophy", wantLoser: "filosofia",
		},
		{
			name:      "an unrecognized direction is a tie too",
			direction: "sideways", a: "filosofia", b: "philosophy",
			wantWinner: "philosophy", wantLoser: "filosofia",
		},
		{
			name: "a deterministic variant carries no direction and resolves through the tie-break",
			a:    "decolonisation", b: "decolonization",
			wantWinner: "decolonisation", wantLoser: "decolonization",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			winner, loser := CanonicalLoser(tc.direction, tc.a, tc.b)
			if winner != tc.wantWinner || loser != tc.wantLoser {
				t.Errorf("CanonicalLoser(%q, %q, %q) = (%q, %q), want (%q, %q)",
					tc.direction, tc.a, tc.b, winner, loser, tc.wantWinner, tc.wantLoser)
			}
			if winner == loser {
				t.Error("a pair must never resolve a name onto itself")
			}
		})
	}

	t.Run("a directionless pair agrees with PreferCanonical", func(t *testing.T) {
		pairs := [][2]string{
			{"filosofia", "philosophy"},
			{"decolonisation", "decolonization"},
			{"20th century", "19th century"},
		}
		for _, p := range pairs {
			winner, loser := CanonicalLoser("", p[0], p[1])
			if want := PreferCanonical(p[0], p[1]); winner != want {
				t.Errorf("CanonicalLoser(\"\") = %q, PreferCanonical = %q: the tie must fall to the shared rule",
					winner, want)
			}
			if loser == winner {
				t.Errorf("pair %v resolved a name onto itself", p)
			}
		}
	})
}

// TestCompositionReview pins the single-read seam behind the two journal views: one query returns both the
// hold reasons and the states, and each view matches what its single-view wrapper returns for the same
// input — a degraded keep appears as a hold and as a state judgment is still owed.
func TestCompositionReview(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	q := client.Queries
	docID := database.CreateUniqueDocument(t, q, "composition-review")

	// a criteria version of this test's own keeps the rows out of every other read
	const version = "review-test-v1"
	seed := func(candidate, disposition, reason string) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx, `
			INSERT INTO tag_composition_event
			    (document_id, candidate, parts, disposition, reason, criteria_version)
			VALUES ($1, $2, '[]'::jsonb, $3, $4, $5)`,
			docID, candidate, disposition, reason, version); err != nil {
			t.Fatalf("seed composition row for %q: %v", candidate, err)
		}
	}
	seed("heldsplit", string(CompositionKeep), CompositionReasonSplitDefer)
	seed("degraded", string(CompositionKeep), CompositionReasonJudgeUnavailable)
	seed("decideddrop", string(CompositionDrop), CompositionReasonNoDimension)
	seed("unrelated", string(CompositionKeep), CompositionReasonNamedUnit)

	names := []string{"heldsplit", "degraded", "decideddrop", "unrelated", "unjudged"}
	held, states, err := CompositionReview(ctx, q, names, version)
	if err != nil {
		t.Fatalf("CompositionReview: %v", err)
	}

	oneHeld, err := CompositionHeldNames(ctx, q, names, version)
	if err != nil {
		t.Fatalf("CompositionHeldNames: %v", err)
	}
	oneStates, err := CompositionStatesForNames(ctx, q, names, version)
	if err != nil {
		t.Fatalf("CompositionStatesForNames: %v", err)
	}
	if !reflect.DeepEqual(held, oneHeld) {
		t.Errorf("held view = %v, want the single-view read %v", held, oneHeld)
	}
	if !reflect.DeepEqual(states, oneStates) {
		t.Errorf("states view = %v, want the single-view read %v", states, oneStates)
	}

	if held["heldsplit"] != CompositionReasonSplitDefer {
		t.Errorf("held[heldsplit] = %q, want the deferred-split hold", held["heldsplit"])
	}
	if held["degraded"] != CompositionReasonJudgeUnavailable {
		t.Errorf("held[degraded] = %q, want the degraded-keep hold", held["degraded"])
	}
	if got, want := states["degraded"], (CompositionState{Disposition: CompositionKeep, Decided: false}); got != want {
		t.Errorf("states[degraded] = %+v, want %+v: judgment is still owed", got, want)
	}
	if got, want := states["decideddrop"], (CompositionState{Disposition: CompositionDrop, Decided: true}); got != want {
		t.Errorf("states[decideddrop] = %+v, want %+v", got, want)
	}
	if _, ok := states["unjudged"]; ok {
		t.Errorf("states[unjudged] = %v, a compound with no row must stay absent", states["unjudged"])
	}
	if _, ok := held["unrelated"]; ok {
		t.Errorf("held[unrelated] = %q, a clean keep is not a hold", held["unrelated"])
	}

	emptyHeld, emptyStates, err := CompositionReview(ctx, q, nil, version)
	if err != nil {
		t.Fatalf("CompositionReview(nil): %v", err)
	}
	if len(emptyHeld) != 0 || len(emptyStates) != 0 {
		t.Errorf("empty input = (%v, %v), want two empty maps", emptyHeld, emptyStates)
	}
}
