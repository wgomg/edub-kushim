package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

func TestEnrichTaskHandlerDedupKey(t *testing.T) {
	h := &EnrichTaskHandler{}

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"document id", `{"document_id":"doc-9"}`, "enrich:doc:doc-9"},
		{"missing document id", `{"other":"field"}`, ""},
		{"undecodable payload", `{not json`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := h.DedupKey(json.RawMessage(tt.payload)); got != tt.want {
				t.Errorf("DedupKey(%s) = %q, want %q", tt.payload, got, tt.want)
			}
		})
	}
}

func TestEnrichResultEnvelope(t *testing.T) {
	counts := DocumentCounts{Assignments: 2, Births: 1, Promotions: 1, Rejects: 1, Defers: 1, Splits: 1, Keeps: 1, Drops: 1}

	t.Run("round trip preserves the counts", func(t *testing.T) {
		payload, err := marshalEnrichResult(json.RawMessage(`{"prompt_tokens":7}`), counts)
		if err != nil {
			t.Fatalf("marshalEnrichResult: %v", err)
		}
		got, ok := CountsFromResult(payload)
		if !ok {
			t.Fatal("CountsFromResult rejected a fresh envelope")
		}
		if got != counts {
			t.Errorf("counts = %+v, want %+v", got, counts)
		}
	})

	t.Run("nil stats serializes as null and still decodes", func(t *testing.T) {
		payload, err := marshalEnrichResult(nil, counts)
		if err != nil {
			t.Fatalf("marshalEnrichResult: %v", err)
		}
		if !strings.Contains(string(payload), `"stats":null`) {
			t.Errorf("payload = %s, want stats null", payload)
		}
		if _, ok := CountsFromResult(payload); !ok {
			t.Error("CountsFromResult rejected a nil-stats envelope")
		}
	})

	t.Run("pre-envelope payload yields no counts", func(t *testing.T) {
		if _, ok := CountsFromResult(json.RawMessage(`{"prompt_tokens":7}`)); ok {
			t.Error("the bare stats payload written before the envelope must not decode as counts")
		}
	})

	t.Run("undecodable payload yields no counts", func(t *testing.T) {
		if _, ok := CountsFromResult(json.RawMessage(`{not json`)); ok {
			t.Error("an undecodable payload must not decode as counts")
		}
	})

	t.Run("stats that are not valid JSON fail the marshal", func(t *testing.T) {
		payload, err := marshalEnrichResult(json.RawMessage(`{broken`), counts)
		if err == nil {
			t.Fatal("invalid stats must fail the marshal instead of storing a payload without them")
		}
		if payload != nil {
			t.Errorf("payload = %s, want nil on failure", payload)
		}
	})
}

func TestDocumentCounts(t *testing.T) {
	results := []types.CreateResult[database.Tag]{
		{Status: types.Created},
		{Status: types.Created},
		{Status: types.Conflict},
		{Status: types.Apply},
	}
	decisions := map[string]tags.AdmissionDecision{
		"admitted": {Outcome: tags.AdmissionAdmit, Reason: tags.ReasonAdmitted},
		"deferred": {Outcome: tags.AdmissionDefer, Reason: tags.ReasonSingleSignal},
	}
	rejected := map[string]bool{"first drop": true, "second drop": true}
	rows := []tags.CompositionRow{
		{Disposition: tags.CompositionSplit},
		{Disposition: tags.CompositionSplit},
		{Disposition: tags.CompositionKeep},
		{Disposition: tags.CompositionDrop},
	}

	got := documentCounts(results, []int64{7, 8}, []string{"seeded"}, decisions, rejected, rows)
	want := DocumentCounts{
		Assignments: 2,
		Births:      2,
		Promotions:  1,
		Rejects:     2,
		Defers:      1,
		Splits:      2,
		Keeps:       1,
		Drops:       1,
	}
	if got != want {
		t.Errorf("documentCounts() = %+v, want %+v", got, want)
	}

	// the quality layer off must leave its fields zero, not misread nil
	off := documentCounts(results, []int64{7}, nil, nil, nil, nil)
	wantOff := DocumentCounts{Assignments: 1, Births: 2}
	if off != wantOff {
		t.Errorf("documentCounts() with the quality layer off = %+v, want %+v", off, wantOff)
	}
}

func TestDocumentCountsAdd(t *testing.T) {
	left := DocumentCounts{Assignments: 1, Births: 2, Promotions: 3, Rejects: 4, Defers: 5, Splits: 6, Keeps: 7, Drops: 8}
	right := DocumentCounts{Assignments: 10, Births: 20, Promotions: 30, Rejects: 40, Defers: 50, Splits: 60, Keeps: 70, Drops: 80}

	left.Add(right)

	want := DocumentCounts{Assignments: 11, Births: 22, Promotions: 33, Rejects: 44, Defers: 55, Splits: 66, Keeps: 77, Drops: 88}
	if left != want {
		t.Errorf("Add() = %+v, want %+v", left, want)
	}
}

// barMatcher answers Match with a fixed candidate list so the test controls
// every similarity the injection bar sees.
type barMatcher struct {
	matches []matcher.Candidate
}

func (m *barMatcher) Match(context.Context, string, string) ([]matcher.Candidate, error) {
	return m.matches, nil
}

func (m *barMatcher) Rank(context.Context, string, []string) ([]matcher.RankResult, error) {
	return nil, nil
}

func (m *barMatcher) SimilarPairs(context.Context) ([]matcher.SimilarPair, error) {
	return nil, nil
}

func (m *barMatcher) Close() {}

func (m *barMatcher) Name() string { return "bar-matcher" }

// suggestionRecorder stands in for the content analyzer and stops the
// enricher at the emission seam, recording what the bar let through.
type suggestionRecorder struct {
	calls       int
	suggestions []string
}

func (r *suggestionRecorder) Analyze(_ context.Context, _ string, _, _ []analyzer.TypeRef, tagSuggestions []string) (*analyzer.AnalysisResult, error) {
	r.calls++
	r.suggestions = append([]string(nil), tagSuggestions...)
	return nil, errors.New("analysis stops at the suggestion seam")
}

func (r *suggestionRecorder) AnalyzeDocType(context.Context, *analyzer.AnalysisResult, string, []analyzer.TypeRef, analyzer.DocMetadata) (string, error) {
	return "", nil
}

func (r *suggestionRecorder) Adjudicate(context.Context, []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
	return nil, nil
}

func (r *suggestionRecorder) AdjudicateTagSplits(context.Context, []string) ([]analyzer.TagSplitResult, error) {
	return nil, nil
}

func (r *suggestionRecorder) JudgeTagQuality(context.Context, string, []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error) {
	return nil, nil
}

func (r *suggestionRecorder) JudgeTagIdentity(context.Context, []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
	return nil, nil
}

func (r *suggestionRecorder) Name() string { return "suggestion-recorder" }

// TestEnrich_SuggestionInjectionBar drives one enrichment pass to the
// analysis seam and asserts what the matcher suggestions the injection bar
// let through, including the designed empty result when nothing clears it.
func TestEnrich_SuggestionInjectionBar(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	logger := log.NewDiscardLogger()

	newEnricher := func(t *testing.T, bar float64, matches []matcher.Candidate) (*Enricher, *suggestionRecorder) {
		t.Helper()
		client := database.NewTestClient(t)
		cfg := &config.Config{
			Enricher: config.EnricherConfig{
				TagMatcher: config.TagMatcherConfig{MinSuggestionSimilarity: bar},
			},
		}
		recorder := &suggestionRecorder{}
		e := NewEnricher(cfg, logger, client, curation.NewTag(client, logger, nil), &barMatcher{matches: matches})
		setUnexportedField(t, e.runner, "contentAnalyzer", recorder)
		return e, recorder
	}
	doc := database.Document{
		DocumentID:  "suggestion-bar-doc",
		StoragePath: "suggestion-bar.pdf",
		TextContent: sql.NullString{String: "short text about spudger repair", Valid: true},
		WordCount:   6,
	}
	run := func(t *testing.T, e *Enricher) error {
		t.Helper()
		_, _, err := e.Enrich(context.Background(), doc, task.NoopProgress)
		return err
	}

	t.Run("only matches at or above the bar reach the emission seam", func(t *testing.T) {
		e, recorder := newEnricher(t, 0.55, []matcher.Candidate{
			{Tag: "bar keep candidate", Similarity: 0.61},
			{Tag: "bar drop one", Similarity: 0.53},
			{Tag: "bar drop two", Similarity: 0.44},
		})

		err := run(t, e)

		if err == nil {
			t.Error("the recorder's stop error must surface from Enrich")
		}
		if recorder.calls == 0 {
			t.Fatal("the analysis stage was never reached")
		}
		if want := []string{"bar keep candidate"}; !slices.Equal(recorder.suggestions, want) {
			t.Errorf("suggestions = %v, want the above-bar match %v", recorder.suggestions, want)
		}
	})

	t.Run("nothing clears the bar and an empty list is injected", func(t *testing.T) {
		e, recorder := newEnricher(t, 0.55, []matcher.Candidate{
			{Tag: "bar drop one", Similarity: 0.50},
			{Tag: "bar drop two", Similarity: 0.44},
		})

		err := run(t, e)

		if err == nil {
			t.Error("the recorder's stop error must surface from Enrich")
		}
		if recorder.calls == 0 {
			t.Fatal("the analysis stage was never reached")
		}
		if len(recorder.suggestions) != 0 {
			t.Errorf("suggestions = %v, want none when every match is below the bar", recorder.suggestions)
		}
	})
}

// setUnexportedField injects a runner capability the constructor cannot
// build in a test: the runner fields live in package tools.
func setUnexportedField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	if !v.IsValid() {
		t.Fatalf("field %q not found", fieldName)
	}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func TestCreditPause(t *testing.T) {
	creditErr := &analyzer.InsufficientCreditsError{Provider: "test", HTTPStatus: 402}
	tests := []struct {
		name      string
		pauseFlag bool
		err       error
		wantNil   bool
		wantPause bool
	}{
		{"credit exhaustion pauses the batch when enabled", true, creditErr, false, true},
		{"credit exhaustion only fails the task when disabled", false, creditErr, false, false},
		{"other errors never pause", true, errors.New("boom"), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Enricher{config: &config.Config{
				Enricher: config.EnricherConfig{
					ContentAnalyzer: config.ContentAnalyzerConfig{PauseOnCreditError: tt.pauseFlag},
				},
			}}

			te := e.creditPause("req-1", tt.err)
			if tt.wantNil {
				if te != nil {
					t.Fatalf("creditPause() = %+v, want nil for a non-credit error", te)
				}
				return
			}
			if te == nil {
				t.Fatal("creditPause() = nil, want the batch pause task error")
			}
			if te.ReqID != "req-1" {
				t.Errorf("ReqID = %q, want req-1", te.ReqID)
			}
			if te.PauseBatch != tt.wantPause {
				t.Errorf("PauseBatch = %t, want %t", te.PauseBatch, tt.wantPause)
			}
			if !strings.Contains(te.Err.Error(), "LLM credit exhausted (test)") {
				t.Errorf("Err = %q, want the provider-tagged credit message", te.Err.Error())
			}
		})
	}
}
