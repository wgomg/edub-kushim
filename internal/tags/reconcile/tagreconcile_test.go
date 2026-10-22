package reconcile

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/tags/curation"
)

// TestTagReconcileDedupKey pins the key the task type definition derives: the
// per-type slot that lets an active run block the next trigger.
func TestTagReconcileDedupKey(t *testing.T) {
	if got, want := curation.TagReconcileDedupKey, "tagreconcile"; got != want {
		t.Fatalf("TagReconcileDedupKey = %q, want %q: the dedup slot is per type", got, want)
	}
}
