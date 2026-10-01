package tags

import "testing"

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
			view:     StoreView{Names: []string{"buildings"}},
			input:    "building",
			wantDisp: DispositionRegister,
			wantName: "building",
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
