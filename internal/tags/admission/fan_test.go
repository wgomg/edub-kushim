package admission

import (
	"reflect"
	"testing"

	"github.com/wgomg/edub-kushim/internal/tags"
)

// TestResolveContentSingles pins the singles resolver the decomposition fan is built from: an unseeded time
// designation is emptied, a store-held one is exempt, an attested adjective folds onto its anchor, and the
// two slices stay aligned with DeriveParts so a caller can index them together.
func TestResolveContentSingles(t *testing.T) {
	cases := []struct {
		name      string
		candidate string
		promoted  map[string]string
		live      map[string]struct{}
		want      []string
	}{
		{
			name:      "an unseeded month is emptied",
			candidate: "october revolution",
			want:      []string{"", "revolution"},
		},
		{
			name:      "a store-held time word is exempt from the gate",
			candidate: "may 1968 celebration",
			live:      map[string]struct{}{tags.Key("may"): {}, tags.Key("1968"): {}},
			want:      []string{"may", "1968", "celebration"},
		},
		{
			name:      "an attested adjective folds onto its anchor",
			candidate: "mexican revolution",
			promoted:  map[string]string{tags.Key("mexico"): "mexico"},
			live:      map[string]struct{}{tags.Key("mexico"): {}},
			want:      []string{"mexico", "revolution"},
		},
		{
			name:      "function words are not singles",
			candidate: "the war of the worlds",
			want:      []string{"war", "worlds"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, singles := tags.ResolvedContent(tc.candidate, tc.promoted, tc.live)
			if !reflect.DeepEqual(content, tags.DeriveParts(tc.candidate)) {
				t.Errorf("content = %v, want DeriveParts(%q) = %v", content, tc.candidate, tags.DeriveParts(tc.candidate))
			}
			if !reflect.DeepEqual(singles, tc.want) {
				t.Errorf("singles = %v, want %v", singles, tc.want)
			}
		})
	}
}

// TestBuildFan pins the fan itself: the singles and the contiguous bigrams resolve, the bigram is literal,
// a nil singles slice leaves every word unresolved, a dropped word removes its bigrams, and a bigram that
// is itself a time designation is not a segment.
func TestBuildFan(t *testing.T) {
	t.Run("singles and contiguous bigrams resolve", func(t *testing.T) {
		fan := buildFan(
			[]string{"object", "oriented", "programming"},
			[]string{"object", "oriented", "programming"},
			nil,
		)
		want := map[string]string{
			"object":               "object",
			"oriented":             "oriented",
			"programming":          "programming",
			"object oriented":      "object oriented",
			"oriented programming": "oriented programming",
		}
		if !reflect.DeepEqual(fan.Resolved, want) {
			t.Errorf("fan segments = %v, want %v", fan.Resolved, want)
		}
		if !reflect.DeepEqual(fan.Content, []string{"object", "oriented", "programming"}) {
			t.Errorf("fan content = %v, want the content words", fan.Content)
		}
	})

	t.Run("nil singles leave every word unresolved", func(t *testing.T) {
		fan := buildFan([]string{"object", "oriented"}, nil, nil)
		if len(fan.Resolved) != 0 {
			t.Errorf("fan segments = %v, want none", fan.Resolved)
		}
	})

	t.Run("a dropped word removes its bigrams", func(t *testing.T) {
		fan := buildFan([]string{"october", "revolution"}, []string{"", "revolution"}, nil)
		want := map[string]string{"revolution": "revolution"}
		if !reflect.DeepEqual(fan.Resolved, want) {
			t.Errorf("fan segments = %v, want only the surviving word %v", fan.Resolved, want)
		}
	})

	t.Run("a time-designated bigram is not a segment", func(t *testing.T) {
		fan := buildFan([]string{"may", "1968"}, []string{"may", "1968"}, nil)
		want := map[string]string{"may": "may", "1968": "1968"}
		if !reflect.DeepEqual(fan.Resolved, want) {
			t.Errorf("fan segments = %v, want no time-designated bigram: %v", fan.Resolved, want)
		}
	})
}

// TestPhraseFoldKeys pins which words raise a residual fold question: only an unresolved word (not
// time-dropped, not mechanically folded) whose primary-sense base is not live while another attested base is.
func TestPhraseFoldKeys(t *testing.T) {
	live := map[string]struct{}{tags.Key("state"): {}}

	t.Run("a residual word raises its live base", func(t *testing.T) {
		keys := phraseFoldKeys([]string{"civil", "war"}, []string{"civil", "war"}, "civil war", live)
		want := []PhraseFoldKey{{Phrase: "civil war", Part: "civil", Anchor: "state"}}
		if !reflect.DeepEqual(keys, want) {
			t.Errorf("phraseFoldKeys = %v, want %v", keys, want)
		}
	})

	t.Run("a folded word raises no question", func(t *testing.T) {
		keys := phraseFoldKeys([]string{"civil", "war"}, []string{"state", "war"}, "civil war", live)
		if len(keys) != 0 {
			t.Errorf("phraseFoldKeys = %v, want none for an already-resolved word", keys)
		}
	})

	t.Run("a time-dropped word raises no question", func(t *testing.T) {
		keys := phraseFoldKeys([]string{"october", "war"}, []string{"", "war"}, "october war", live)
		if len(keys) != 0 {
			t.Errorf("phraseFoldKeys = %v, want none for a dropped word", keys)
		}
	})

	t.Run("a word with a live primary base raises no question", func(t *testing.T) {
		live := map[string]struct{}{tags.Key("citizen"): {}}
		keys := phraseFoldKeys([]string{"civil", "war"}, []string{"civil", "war"}, "civil war", live)
		if len(keys) != 0 {
			t.Errorf("phraseFoldKeys = %v, want none while the primary base is live", keys)
		}
	})
}

// TestApplyPhraseFolds pins the single fold-application rule: a confirmed answer folds the word onto its
// anchor, an abstention or a several-live-bases ambiguity defers it, and a missing verdict leaves it unfolded
// for the judged identity path.
func TestApplyPhraseFolds(t *testing.T) {
	live := map[string]struct{}{tags.Key("state"): {}}

	t.Run("a confirmed fold applies the anchor", func(t *testing.T) {
		verdicts := map[PhraseFoldKey]PhraseFoldVerdict{
			{Phrase: "civil war", Part: "civil", Anchor: "state"}: {Fold: true},
		}
		singles, deferred := applyPhraseFolds([]string{"civil", "war"}, []string{"civil", "war"}, "civil war", verdicts, live)
		if !reflect.DeepEqual(singles, []string{"state", "war"}) {
			t.Errorf("singles = %v, want the anchor applied", singles)
		}
		if len(deferred) != 0 {
			t.Errorf("deferred = %v, want none for a confirmed fold", deferred)
		}
	})

	t.Run("an abstention defers the word", func(t *testing.T) {
		verdicts := map[PhraseFoldKey]PhraseFoldVerdict{
			{Phrase: "civil war", Part: "civil", Anchor: "state"}: {Deferred: true},
		}
		singles, deferred := applyPhraseFolds([]string{"civil", "war"}, []string{"civil", "war"}, "civil war", verdicts, live)
		if !reflect.DeepEqual(singles, []string{"civil", "war"}) {
			t.Errorf("singles = %v, want the word left unfolded", singles)
		}
		if !reflect.DeepEqual(deferred, []string{"civil"}) {
			t.Errorf("deferred = %v, want the abstained word", deferred)
		}
	})

	t.Run("several live bases defer without a verdict", func(t *testing.T) {
		ambiguous := map[string]struct{}{tags.Key("state"): {}, tags.Key("country"): {}}
		singles, deferred := applyPhraseFolds([]string{"civil", "war"}, []string{"civil", "war"}, "civil war", nil, ambiguous)
		if !reflect.DeepEqual(singles, []string{"civil", "war"}) {
			t.Errorf("singles = %v, want the word left unfolded", singles)
		}
		if !reflect.DeepEqual(deferred, []string{"civil"}) {
			t.Errorf("deferred = %v, want the ambiguous word", deferred)
		}
	})
}

// TestCoverParts pins the segments the composition decision reads: a full multi-segment cover returns the
// cover's applied names, anything shorter returns the flat resolved content so the unit branch still gets
// its deferred/unavailable reads.
func TestCoverParts(t *testing.T) {
	t.Run("a full cover returns the cover segments", func(t *testing.T) {
		fan := buildFan([]string{"object", "oriented", "programming"},
			[]string{"object", "oriented", "programming"}, nil)
		verdicts := map[string]PartVerdict{
			"object oriented": {Dimensional: true},
			"programming":     {Dimensional: true},
		}
		parts, complete := coverParts(fan, nil, verdicts)
		if !complete {
			t.Fatal("a full multi-segment cover must read complete")
		}
		if !reflect.DeepEqual(parts, []string{"object oriented", "programming"}) {
			t.Errorf("parts = %v, want the cover's applied names", parts)
		}
	})

	t.Run("a single usable span leaves the unit branch", func(t *testing.T) {
		fan := buildFan([]string{"black", "bloc"}, []string{"black", "bloc"}, nil)
		verdicts := map[string]PartVerdict{"black bloc": {Dimensional: true}}
		parts, complete := coverParts(fan, nil, verdicts)
		if complete {
			t.Error("a whole-sequence bigram is not a decomposition; the unit branch must own it")
		}
		if !reflect.DeepEqual(parts, []string{"black", "bloc"}) {
			t.Errorf("parts = %v, want the flat resolved content", parts)
		}
	})

	t.Run("a fan abstention blocks the word's span", func(t *testing.T) {
		fan := buildFan([]string{"object", "oriented", "programming"},
			[]string{"object", "oriented", "programming"}, nil)
		fan.deferred = []string{"programming"}
		verdicts := map[string]PartVerdict{
			"object oriented": {Dimensional: true},
			"programming":     {Dimensional: true},
		}
		parts, complete := coverParts(fan, nil, verdicts)
		if complete {
			t.Error("a word the phrase-context fold abstained on must never lend a cover")
		}
		if !reflect.DeepEqual(parts, []string{"object", "oriented", "programming"}) {
			t.Errorf("parts = %v, want the flat resolved content", parts)
		}
	})
}

// TestSeedStoreValues pins the store-value seeding: a segment the store already holds reads dimensional
// without a parts verdict, and an existing verdict is never overwritten.
func TestSeedStoreValues(t *testing.T) {
	fan := buildFan([]string{"civil", "war"}, []string{"civil", "war"}, nil)
	verdicts := map[string]PartVerdict{"war": {}}
	storeKeys := map[string]struct{}{tags.Key("civil war"): {}, tags.Key("war"): {}}

	seedStoreValues(verdicts, []Fan{fan}, storeKeys)

	if v, ok := verdicts["civil war"]; !ok || !v.Dimensional {
		t.Errorf("store value verdict = %+v, want it seeded dimensional", v)
	}
	if v := verdicts["war"]; v.Dimensional {
		t.Errorf("an existing verdict was overwritten: %+v", v)
	}
	if _, ok := verdicts["civil"]; ok {
		t.Error("a segment the store does not hold must not be seeded")
	}
}
