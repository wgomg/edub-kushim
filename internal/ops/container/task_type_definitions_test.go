package container

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestDedupKeys pins the derivation each task type owns: the enqueue derives the key from the definition
// and nothing else, so these values are the dedup contract for every boundary.
func TestDedupKeys(t *testing.T) {
	catalog := NewCatalog(nil)

	tests := []struct {
		name     string
		taskType types.TaskType
		payload  string
		want     string
	}{
		{"consume content hash", types.Task.Type.Consume, `{"md5":"abc"}`, "consume:abc"},
		{"consume without hash", types.Task.Type.Consume, `{"file_path":"/tmp/x.pdf"}`, ""},
		{"enrich document id", types.Task.Type.Enrich, `{"document_id":"doc-9"}`, "enrich:doc:doc-9"},
		{"enrich missing document id", types.Task.Type.Enrich, `{"other":"field"}`, ""},
		{"enrich undecodable payload", types.Task.Type.Enrich, `{not json`, ""},
		{"thumbnail document id", types.Task.Type.Thumbnail, `{"document_id":"doc-9"}`, "thumbnail:doc:doc-9"},
		{"thumbnail child without document id", types.Task.Type.Thumbnail, `{"waiting_for":"task-1"}`, ""},
		{"thumbbackfill document id", types.Task.Type.ThumbBackfill, `{"document_id":"doc-9"}`, "thumbnail:doc:doc-9"},
		{"tag reconcile", types.Task.Type.TagReconcile, `{}`, curation.TagReconcileDedupKey},
		{"tag measure", types.Task.Type.TagMeasure, `{}`, tags.TagMeasureDedupKey},
		{"tag identity", types.Task.Type.TagIdentity, `{}`, curation.TagIdentityDedupKey},
		{"tag audit", types.Task.Type.TagAudit, `{}`, curation.TagAuditDedupKey},
		{"config tessdata", types.Task.Type.Config, `{"op":"tessdata","lang":"eng"}`, "config:tessdata:eng"},
		{"config hugot", types.Task.Type.Config, `{"op":"hugot"}`, "config:hugot"},
		{"config unknown op", types.Task.Type.Config, `{}`, ""},
		{"migrate-db", types.Task.Type.MigrateDB, `{}`, configtask.DedupKeyMigrateDB},
		{"migrate-storage", types.Task.Type.MigrateStorage, `{}`, configtask.DedupKeyMigrateStorage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := catalog.Definition(tt.taskType)
			if err != nil {
				t.Fatalf("definition for %q: %v", tt.taskType, err)
			}
			if def.DedupKey == nil {
				t.Fatalf("%q has no dedup derivation", tt.taskType)
			}
			if got := def.DedupKey(json.RawMessage(tt.payload)); got != tt.want {
				t.Fatalf("DedupKey(%s) = %q, want %q", tt.payload, got, tt.want)
			}
		})
	}
}

// TestLockDeclarationsMatchVocabulary pins the definitions' queued lock policy to the closed vocabulary
// table: the enqueue validates common holds through the definitions while the claim, drain and sweep
// paths read the table, so neither side may drift. Direct mode defaults to the queued set everywhere
// until a type's direct path declares otherwise.
func TestLockDeclarationsMatchVocabulary(t *testing.T) {
	catalog := NewCatalog(nil)

	for _, taskType := range types.AllTaskTypes() {
		def, err := catalog.Definition(taskType)
		if err != nil {
			t.Fatalf("definition for %q: %v", taskType, err)
		}
		declaration := types.LockDeclarationFor(taskType)
		if !equalLockNames(def.Locks.Holds, declaration.Holds) {
			t.Errorf("%q holds = %v, want %v", taskType, def.Locks.Holds, declaration.Holds)
		}
		if !equalLockNames(def.Locks.WaitsFor, declaration.WaitsFor) {
			t.Errorf("%q waits = %v, want %v", taskType, def.Locks.WaitsFor, declaration.WaitsFor)
		}
		if def.DirectLocks != nil {
			t.Errorf("%q direct locks = %+v, want nil (defaults to the queued set)", taskType, *def.DirectLocks)
		}
	}
}

func equalLockNames(a, b []types.LockName) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDailyDedupKeys pins the date-scoped keys of the daily backup and mirror runs.
func TestDailyDedupKeys(t *testing.T) {
	catalog := NewCatalog(nil)
	dateSuffix := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	t.Run("backup keeps the mode and the UTC day", func(t *testing.T) {
		def, err := catalog.Definition(types.Task.Type.Backup)
		if err != nil {
			t.Fatalf("backup definition: %v", err)
		}
		got := def.DedupKey(json.RawMessage(`{"mode":"database"}`))
		prefix := "backup:database:"
		if len(got) <= len(prefix) || got[:len(prefix)] != prefix || !dateSuffix.MatchString(got[len(prefix):]) {
			t.Fatalf("backup key = %q, want %q<YYYY-MM-DD>", got, prefix)
		}
	})

	t.Run("backup defaults the mode to full", func(t *testing.T) {
		def, _ := catalog.Definition(types.Task.Type.Backup)
		got := def.DedupKey(json.RawMessage(`{}`))
		if len(got) < len("backup:full:") || got[:len("backup:full:")] != "backup:full:" {
			t.Fatalf("backup key = %q, want backup:full:<date>", got)
		}
	})

	t.Run("mirror keeps the UTC day", func(t *testing.T) {
		def, err := catalog.Definition(types.Task.Type.Mirror)
		if err != nil {
			t.Fatalf("mirror definition: %v", err)
		}
		got := def.DedupKey(json.RawMessage(`{}`))
		if len(got) <= len("mirror:") || got[:len("mirror:")] != "mirror:" || !dateSuffix.MatchString(got[len("mirror:"):]) {
			t.Fatalf("mirror key = %q, want mirror:<YYYY-MM-DD>", got)
		}
	})
}
