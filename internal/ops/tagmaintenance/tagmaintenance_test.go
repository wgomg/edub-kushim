package tagmaintenance

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
)

// TestRoutineDedupKeysAreScoped pins the routine's activation-key vocabulary: each chained step keeps a key
// distinct from the daily or manually enqueued pass, so a pending pass of that type cannot collide with the
// routine's activation and discard the step together with its waiting branch.
func TestRoutineDedupKeysAreScoped(t *testing.T) {
	tests := []struct {
		name      string
		got       string
		want      string
		sharedKey string
	}{
		{"reconcile", routineReconcileDedupKey, "tagmaintenance:tagreconcile", curation.TagReconcileDedupKey},
		{"identity", routineIdentityDedupKey, "tagmaintenance:tagidentity", curation.TagIdentityDedupKey},
		{"audit", routineAuditDedupKey, "tagmaintenance:tagaudit", curation.TagAuditDedupKey},
		{"measure", routineMeasureDedupKey, "tagmaintenance:tagmeasure", tags.TagMeasureDedupKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s scoped key = %q, want %q", tt.name, tt.got, tt.want)
			}
			if tt.got == tt.sharedKey {
				t.Fatalf("%s scoped key %q equals the shared pass key: activation could discard the branch",
					tt.name, tt.got)
			}
		})
	}
}
