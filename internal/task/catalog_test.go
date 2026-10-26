package task_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// productionHooks wires every type's producer-owned dedup derivation, the way a process boundary that
// enqueues the whole task vocabulary builds its catalog.
func productionHooks() map[types.TaskType]task.DedupKeyFunc {
	return map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.Consume:        consumption.DedupKey,
		types.Task.Type.Enrich:         enrichment.DedupKey,
		types.Task.Type.Thumbnail:      service.ThumbnailDedupKey,
		types.Task.Type.ThumbBackfill:  service.ThumbnailDedupKey,
		types.Task.Type.Backup:         backup.BackupDedupKey,
		types.Task.Type.Mirror:         backup.MirrorDedupKey,
		types.Task.Type.TagReconcile:   task.ConstantDedupKey(curation.TagReconcileDedupKey),
		types.Task.Type.TagMeasure:     task.ConstantDedupKey(tags.TagMeasureDedupKey),
		types.Task.Type.TagIdentity:    task.ConstantDedupKey(curation.TagIdentityDedupKey),
		types.Task.Type.TagAudit:       task.ConstantDedupKey(curation.TagAuditDedupKey),
		types.Task.Type.MigrateDB:      task.ConstantDedupKey(configtask.DedupKeyMigrateDB),
		types.Task.Type.MigrateStorage: task.ConstantDedupKey(configtask.DedupKeyMigrateStorage),
	}
}

func testCatalog(t *testing.T) *task.Catalog {
	t.Helper()
	catalog, err := task.NewCatalog(productionHooks(), nil)
	if err != nil {
		t.Fatalf("build task catalog: %v", err)
	}
	return catalog
}

// TestDedupKeys pins the derivation each task type owns: the enqueue derives the key from the definition
// and nothing else, so these values are the dedup contract for every boundary.
func TestDedupKeys(t *testing.T) {
	catalog := testCatalog(t)

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
// paths read the table, so neither side may drift. Direct mode defaults to the queued set except where a
// type's direct path overrides it: consume and enrich hold maintenance, thumbnails holds storage and
// waits for maintenance.
func TestLockDeclarationsMatchVocabulary(t *testing.T) {
	catalog := testCatalog(t)

	directOverrides := map[types.TaskType]struct {
		holds []types.LockName
		waits []types.LockName
	}{
		types.Task.Type.Consume: {
			holds: []types.LockName{types.LockMaintenance},
		},
		types.Task.Type.Enrich: {
			holds: []types.LockName{types.LockMaintenance},
		},
		types.Task.Type.Thumbnail: {
			holds: []types.LockName{types.LockStorage},
			waits: []types.LockName{types.LockMaintenance},
		},
	}

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

		override, hasOverride := directOverrides[taskType]
		if !hasOverride {
			if def.DirectLocks != nil {
				t.Errorf("%q direct locks = %+v, want nil (defaults to the queued set)", taskType, *def.DirectLocks)
			}
			continue
		}
		if def.DirectLocks == nil {
			t.Errorf("%q has no direct lock override", taskType)
			continue
		}
		if !equalLockNames(def.DirectLocks.Holds, override.holds) {
			t.Errorf("%q direct holds = %v, want %v", taskType, def.DirectLocks.Holds, override.holds)
		}
		if !equalLockNames(def.DirectLocks.WaitsFor, override.waits) {
			t.Errorf("%q direct waits = %v, want %v", taskType, def.DirectLocks.WaitsFor, override.waits)
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
	catalog := testCatalog(t)
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

// TestNewCatalogRejectsUndeclaredWiringKeys pins that a wiring typo fails at construction.
func TestNewCatalogRejectsUndeclaredWiringKeys(t *testing.T) {
	if _, err := task.NewCatalog(map[types.TaskType]task.DedupKeyFunc{
		types.TaskType("bogus"): nil,
	}, nil); err == nil {
		t.Fatal("an undeclared dedup-hook key must be refused at construction")
	}
	if _, err := task.NewCatalog(nil, map[types.TaskType]int{
		types.TaskType("bogus"): 4,
	}); err == nil {
		t.Fatal("an undeclared concurrency key must be refused at construction")
	}
}

// TestNewCatalogPartialWiring pins the per-boundary contract: an unwired type still resolves its static
// policy, with a nil hook the enqueue surface refuses at use rather than a missing definition.
func TestNewCatalogPartialWiring(t *testing.T) {
	catalog, err := task.NewCatalog(map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.Consume: func(json.RawMessage) string { return "consume-key" },
	}, nil)
	if err != nil {
		t.Fatalf("build task catalog: %v", err)
	}

	consumeDef, err := catalog.Definition(types.Task.Type.Consume)
	if err != nil {
		t.Fatalf("consume definition: %v", err)
	}
	if consumeDef.DedupKey == nil {
		t.Fatal("the wired type must carry its hook")
	}
	if consumeDef.CLI != "consume" {
		t.Fatalf("consume CLI = %q, want consume", consumeDef.CLI)
	}

	backupDef, err := catalog.Definition(types.Task.Type.Backup)
	if err != nil {
		t.Fatalf("unwired type must still resolve: %v", err)
	}
	if backupDef.DedupKey != nil {
		t.Fatal("an unwired type carries no hook; the enqueue refuses its pending tasks")
	}
	if backupDef.Type != types.Task.Type.Backup {
		t.Fatalf("backup type = %q, want the declared type", backupDef.Type)
	}
	declaration := types.LockDeclarationFor(types.Task.Type.Backup)
	if !equalLockNames(backupDef.Locks.Holds, declaration.Holds) {
		t.Fatalf("backup holds = %v, want the queued declaration %v", backupDef.Locks.Holds, declaration.Holds)
	}
}

// TestDirectLockPolicyFor pins the direct-mode lookup: the declared overrides, the queued fallback, and
// no lock for a type outside the static table.
func TestDirectLockPolicyFor(t *testing.T) {
	cases := []struct {
		name  string
		task  types.TaskType
		holds []types.LockName
		waits []types.LockName
	}{
		{name: "consume holds maintenance directly", task: types.Task.Type.Consume, holds: []types.LockName{types.LockMaintenance}},
		{name: "enrich holds maintenance directly", task: types.Task.Type.Enrich, holds: []types.LockName{types.LockMaintenance}},
		{name: "thumbnail overrides to storage and waits", task: types.Task.Type.Thumbnail, holds: []types.LockName{types.LockStorage}, waits: []types.LockName{types.LockMaintenance}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := task.DirectLockPolicyFor(tc.task)
			if !equalLockNames(got.Holds, tc.holds) {
				t.Errorf("holds = %v, want %v", got.Holds, tc.holds)
			}
			if !equalLockNames(got.WaitsFor, tc.waits) {
				t.Errorf("waits = %v, want %v", got.WaitsFor, tc.waits)
			}
		})
	}

	t.Run("a type without an override falls back to its queued declaration", func(t *testing.T) {
		declaration := types.LockDeclarationFor(types.Task.Type.ThumbBackfill)
		got := task.DirectLockPolicyFor(types.Task.Type.ThumbBackfill)
		if !equalLockNames(got.Holds, declaration.Holds) || !equalLockNames(got.WaitsFor, declaration.WaitsFor) {
			t.Fatalf("fallback = %+v, want the queued declaration %+v", got, declaration)
		}
	})

	t.Run("an undeclared type declares no lock", func(t *testing.T) {
		got := task.DirectLockPolicyFor(types.TaskType("bogus"))
		if len(got.Holds) != 0 || len(got.WaitsFor) != 0 {
			t.Fatalf("undeclared type policy = %+v, want no locks", got)
		}
	})
}
