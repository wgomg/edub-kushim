package enrichment

import (
	"encoding/json"
	"testing"
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
