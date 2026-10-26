package task

import (
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/types"
)

// Catalog is the concrete task type table of one process boundary: the static policy declared in this
// package plus the dedup hooks and worker counts that boundary supplies at construction. Enqueue, dispatch
// and the execution library read definitions through it. Its only dependency is internal/types; the values
// that vary between processes arrive as constructor arguments.
type Catalog struct {
	definitions map[types.TaskType]TypeDefinition
}

// staticDefinition is the boundary-independent policy of one task type: identity, the queued and direct
// lock policy, the batch-mode CLI surface and the dispatch host eligibility. Dedup hooks, concurrency and
// preconditions come from the boundary that constructs the catalog.
type staticDefinition struct {
	locks         LockPolicy
	directLocks   *LockPolicy
	cli           string
	batchSelector string
	hosts         []types.Host
}

// staticDefinitions is the closed task policy table. Kushim-hosted workloads run their own CLI in batch
// mode on the queue host; the migrations also declare the setup wizard, which executes their handlers
// in-process while no queue daemon exists. Every other type is queue-only.
var staticDefinitions = map[types.TaskType]staticDefinition{
	types.Task.Type.Consume: {
		locks:         lockPolicy(types.Task.Type.Consume),
		directLocks:   &LockPolicy{Holds: []types.LockName{types.LockMaintenance}},
		cli:           "consume",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.Enrich: {
		locks:         lockPolicy(types.Task.Type.Enrich),
		directLocks:   &LockPolicy{Holds: []types.LockName{types.LockMaintenance}},
		cli:           "enrich",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.Thumbnail: {
		locks: lockPolicy(types.Task.Type.Thumbnail),
		directLocks: &LockPolicy{
			Holds:    []types.LockName{types.LockStorage},
			WaitsFor: []types.LockName{types.LockMaintenance},
		},
		cli:           "thumbnails",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.ThumbBackfill: {
		locks:         lockPolicy(types.Task.Type.ThumbBackfill),
		cli:           "thumbnails",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.Backup: {
		locks:         lockPolicy(types.Task.Type.Backup),
		cli:           "backup",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.Mirror: {
		locks:         lockPolicy(types.Task.Type.Mirror),
		cli:           "mirror",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.TagReconcile: {
		locks:         lockPolicy(types.Task.Type.TagReconcile),
		cli:           "tagreconcile",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.TagMeasure: {
		locks:         lockPolicy(types.Task.Type.TagMeasure),
		cli:           "tagmeasure",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.TagIdentity: {
		locks:         lockPolicy(types.Task.Type.TagIdentity),
		cli:           "tagidentity",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.TagAudit: {
		locks:         lockPolicy(types.Task.Type.TagAudit),
		cli:           "tagaudit",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue},
	},
	types.Task.Type.MigrateDB: {
		locks:         lockPolicy(types.Task.Type.MigrateDB),
		cli:           "migrate",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue, types.HostWizard},
	},
	types.Task.Type.MigrateStorage: {
		locks:         lockPolicy(types.Task.Type.MigrateStorage),
		cli:           "migrate",
		batchSelector: "--from-batch",
		hosts:         []types.Host{types.HostQueue, types.HostWizard},
	},
}

// NewCatalog builds the task type table of one process boundary. dedupHooks and concurrency may be nil
// or partial: they name the types this process enqueues and the worker counts it sizes, so a process
// that never enqueues a type does not import its producer. A pending task of a type without a hook is
// refused by the enqueue surface; a key that is not a declared task type is a wiring error and fails
// here.
func NewCatalog(dedupHooks map[types.TaskType]DedupKeyFunc, concurrency map[types.TaskType]int) (*Catalog, error) {
	for taskType := range dedupHooks {
		if !taskType.Valid() {
			return nil, fmt.Errorf("dedup hook for undeclared task type %q", taskType)
		}
	}
	for taskType := range concurrency {
		if !taskType.Valid() {
			return nil, fmt.Errorf("concurrency for undeclared task type %q", taskType)
		}
	}

	definitions := make(map[types.TaskType]TypeDefinition, len(staticDefinitions))
	for taskType, static := range staticDefinitions {
		definitions[taskType] = TypeDefinition{
			Type:          taskType,
			Locks:         static.locks,
			DirectLocks:   static.directLocks,
			CLI:           static.cli,
			BatchSelector: static.batchSelector,
			Hosts:         static.hosts,
			Concurrency:   concurrency[taskType],
			DedupKey:      dedupHooks[taskType],
		}
	}
	return &Catalog{definitions: definitions}, nil
}

// Definition returns the entry for a task type; an unknown type is an error.
func (c *Catalog) Definition(taskType types.TaskType) (TypeDefinition, error) {
	definition, ok := c.definitions[taskType]
	if !ok {
		return TypeDefinition{}, fmt.Errorf("unknown task type %q", taskType)
	}
	return definition, nil
}

// DirectLockPolicyFor returns the lock policy a direct CLI run of the type uses: its declared direct policy
// when one is set, otherwise the queued lock set. Every call site names a declared type; an undeclared type
// declares no lock.
func DirectLockPolicyFor(taskType types.TaskType) LockPolicy {
	static, ok := staticDefinitions[taskType]
	if !ok {
		return LockPolicy{}
	}
	if static.directLocks != nil {
		return *static.directLocks
	}
	return static.locks
}

// ConstantDedupKey returns a hook that derives the same key from every payload, for the types whose
// dedup vocabulary is a producer-owned constant rather than a payload field.
func ConstantDedupKey(key string) DedupKeyFunc {
	return func(json.RawMessage) string { return key }
}

// lockPolicy adapts the closed lock declaration table to the definition's policy shape.
func lockPolicy(taskType types.TaskType) LockPolicy {
	declaration := types.LockDeclarationFor(taskType)
	return LockPolicy{Holds: declaration.Holds, WaitsFor: declaration.WaitsFor}
}
