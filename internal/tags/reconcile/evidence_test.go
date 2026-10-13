package reconcile

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

type fakeAdjudicator struct {
	analyzer.ContentAnalyzer
	adjudicate func(pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error)
	calls      int
}

func (f *fakeAdjudicator) Adjudicate(_ context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
	f.calls++
	return f.adjudicate(pairs)
}

func setUnexportedField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	if !v.IsValid() {
		t.Fatalf("field %q not found", fieldName)
	}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func newAdjudicateBandHarness(t *testing.T) (*Reconciler, *fakeAdjudicator, *bytes.Buffer, *config.Config) {
	t.Helper()
	fake := &fakeAdjudicator{}
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", log.NewDiscardLogger())
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0}},
	})
	setUnexportedField(t, runner, "tagAdjudicator", fake)

	cfg := &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{Llm: config.LlmConfig{Provider: "test", Model: "judge-model"}},
		},
	}
	var buf bytes.Buffer
	r := &Reconciler{runner: runner, logger: log.NewLoggerWithWriter(&buf)}
	return r, fake, &buf, cfg
}

func bandEntry(query, target string, stored types.TagVerdict) *entry {
	return &entry{query: query, target: target, freshSim: 0.9, verdict: stored}
}

func TestAdjudicateBand_PartialReply(t *testing.T) {
	t.Run("a partial reply salvages the returned verdicts", func(t *testing.T) {
		r, fake, buf, cfg := newAdjudicateBandHarness(t)
		fake.adjudicate = func(pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			results := make([]analyzer.TagVerdictResult, len(pairs))
			results[0] = analyzer.TagVerdictResult{Index: 0, Verdict: types.TagVerdicts.Variant}
			return results, &analyzer.IncompleteReplyError{Missing: []int{1}}
		}
		salvaged := bandEntry("supply", "supply chain", types.TagVerdicts.Variant)
		missing := bandEntry("urban planning", "city planning", types.TagVerdicts.Variant)

		r.adjudicateBand(context.Background(), cfg, []*entry{salvaged, missing})

		if salvaged.skipReason != "" {
			t.Errorf("skipReason = %q, want the salvaged entry left actionable", salvaged.skipReason)
		}
		if salvaged.freshVerdict != types.TagVerdicts.Variant {
			t.Errorf("freshVerdict = %q, want the returned verdict", salvaged.freshVerdict)
		}
		if salvaged.freshModel != "judge-model" {
			t.Errorf("freshModel = %q, want the judge model", salvaged.freshModel)
		}
		if salvaged.reason != "adjudicated" {
			t.Errorf("reason = %q, want adjudicated", salvaged.reason)
		}
		if missing.skipReason != "ambiguous" {
			t.Errorf("skipReason = %q, want the missing entry skipped as ambiguous", missing.skipReason)
		}
		if missing.freshVerdict != "" {
			t.Errorf("freshVerdict = %q, want the missing entry to stay undecided", missing.freshVerdict)
		}
		if fake.calls != 1 {
			t.Errorf("adjudicator calls = %d, want 1", fake.calls)
		}
		out := buf.String()
		if !strings.Contains(out, "partial adjudication reply") || !strings.Contains(out, "salvaged 1 of 2") {
			t.Errorf("expected a WARN naming the partial salvage, got:\n%s", out)
		}
	})

	t.Run("a non-typed failure still skips the whole batch", func(t *testing.T) {
		r, fake, _, cfg := newAdjudicateBandHarness(t)
		fake.adjudicate = func([]analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			return nil, errors.New("provider 500")
		}
		first := bandEntry("supply", "supply chain", types.TagVerdicts.Variant)
		second := bandEntry("urban planning", "city planning", types.TagVerdicts.Variant)

		r.adjudicateBand(context.Background(), cfg, []*entry{first, second})

		if first.skipReason != "adjudication_error" || second.skipReason != "adjudication_error" {
			t.Errorf("skipReasons = %q/%q, want adjudication_error for both", first.skipReason, second.skipReason)
		}
		if first.freshVerdict != "" || second.freshVerdict != "" {
			t.Errorf("freshVerdicts = %q/%q, want both left undecided", first.freshVerdict, second.freshVerdict)
		}
	})

	t.Run("a partial confirmation reply settles only the returned pairs", func(t *testing.T) {
		r, fake, buf, cfg := newAdjudicateBandHarness(t)
		call := 0
		fake.adjudicate = func(pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
			call++
			results := make([]analyzer.TagVerdictResult, len(pairs))
			for i := range results {
				results[i] = analyzer.TagVerdictResult{Index: i, Verdict: types.TagVerdicts.Variant}
			}
			if call == 1 {
				return results, nil
			}
			return results, &analyzer.IncompleteReplyError{Missing: []int{1}}
		}
		confirmed := bandEntry("supply", "supply chain", types.TagVerdicts.Same)
		unconfirmed := bandEntry("urban planning", "city planning", types.TagVerdicts.Same)

		r.adjudicateBand(context.Background(), cfg, []*entry{confirmed, unconfirmed})

		if fake.calls != 2 {
			t.Fatalf("adjudicator calls = %d, want 2 (adjudication then confirmation)", fake.calls)
		}
		if confirmed.skipReason != "" || !confirmed.confirmed {
			t.Errorf("skipReason/confirmed = %q/%t, want the returned pair confirmed", confirmed.skipReason, confirmed.confirmed)
		}
		if unconfirmed.skipReason != "confirmation_ambiguous" || unconfirmed.confirmed {
			t.Errorf("skipReason/confirmed = %q/%t, want the missing pair left unconfirmed", unconfirmed.skipReason, unconfirmed.confirmed)
		}
		out := buf.String()
		if !strings.Contains(out, "partial confirmation reply") || !strings.Contains(out, "salvaged 1 of 2") {
			t.Errorf("expected a WARN naming the partial confirmation salvage, got:\n%s", out)
		}
	})
}

func TestAdjudicateBand_DeterministicVariant(t *testing.T) {
	r, fake, buf, cfg := newAdjudicateBandHarness(t)
	agreesWithStored := bandEntry("decolonisation", "decolonization", types.TagVerdicts.Same)
	disputed := bandEntry("military history", "history military", types.TagVerdicts.Variant)

	r.adjudicateBand(context.Background(), cfg, []*entry{agreesWithStored, disputed})

	if fake.calls != 0 {
		t.Errorf("adjudicator calls = %d, want 0 for mechanically resolved pairs", fake.calls)
	}
	for _, e := range []*entry{agreesWithStored, disputed} {
		if e.skipReason != "" {
			t.Errorf("%q -> %q skipReason = %q, want the entry left actionable", e.query, e.target, e.skipReason)
		}
		if e.freshVerdict != types.TagVerdicts.Same {
			t.Errorf("%q -> %q freshVerdict = %q, want same", e.query, e.target, e.freshVerdict)
		}
		if e.freshModel != "deterministic" {
			t.Errorf("%q -> %q freshModel = %q, want the non-null marker the freshness scans require", e.query, e.target, e.freshModel)
		}
		if e.reason != "deterministic" {
			t.Errorf("%q -> %q reason = %q, want deterministic", e.query, e.target, e.reason)
		}
		if !e.confirmed {
			t.Errorf("%q -> %q not confirmed without a second judge pass", e.query, e.target)
		}
	}
	out := buf.String()
	if !strings.Contains(out, "orthographic") || !strings.Contains(out, "inverted") {
		t.Errorf("expected INFO lines naming both classes, got:\n%s", out)
	}
}
