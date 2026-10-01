package enrichment

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tags"
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
