package reconcile

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/tags/curation"
)

// TestTagReconcileTaskHandlerDedupKey pins the one-key-per-type contract: the
// handler must name the same fixed key the enqueue path writes, whatever payload
// it is handed, so an active run keeps blocking the next trigger.
func TestTagReconcileTaskHandlerDedupKey(t *testing.T) {
	h := NewTagReconcileTaskHandler(nil, nil)

	key := h.DedupKey(json.RawMessage(`{}`))
	if key != curation.TagReconcileDedupKey {
		t.Fatalf("DedupKey = %q, want %q: the enqueue path and the handler must agree on one key", key, curation.TagReconcileDedupKey)
	}
	if other := h.DedupKey(json.RawMessage(`{"reason":"manual"}`)); other != key {
		t.Errorf("DedupKey(payload) = %q, want %q: the slot is per type, not per payload", other, key)
	}
}
