package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/types"
)

// dedupIndexName names the schema's partial unique index on (task_type, dedup_key): only a violation of
// this index is a dedup conflict, while a collision on any other unique constraint (the task id above
// all) must abort the enqueue instead of being skipped.
const dedupIndexName = "idx_task_dedup"

// BatchSpec is the requester metadata every enqueue carries: the entry-point origin and, when scheduled,
// the name of the schedule that produced the run. Its fields stay unexported so only NewBatchSpec can set
// them; the constructor and the enqueue share one validation, so a zero-value spec is rejected too.
type BatchSpec struct {
	origin   types.BatchOrigin
	schedule string
}

// NewBatchSpec validates the origin and schedule supplied by an entry-point boundary and returns the
// spec its enqueue call carries. An empty or unknown origin is rejected rather than defaulted, and a
// schedule name is required with origin schedule and rejected with every other origin, so that only the
// scheduler can produce a scheduled batch.
func NewBatchSpec(origin types.BatchOrigin, schedule string) (BatchSpec, error) {
	spec := BatchSpec{origin: origin, schedule: schedule}
	if err := spec.validate(); err != nil {
		return BatchSpec{}, err
	}
	return spec, nil
}

// validate re-checks the invariant the constructor enforces, so the enqueue can reject a zero-value spec
// from a caller that skipped NewBatchSpec with the same check the constructor applies.
func (s BatchSpec) validate() error {
	if !s.origin.Valid() {
		return fmt.Errorf("batch origin %q is not declared", s.origin)
	}
	if s.origin == types.Batch.Origin.Schedule && s.schedule == "" {
		return fmt.Errorf("batch origin %q requires a schedule name", s.origin)
	}
	if s.origin != types.Batch.Origin.Schedule && s.schedule != "" {
		return fmt.Errorf("batch origin %q carries no schedule, got %q", s.origin, s.schedule)
	}
	return nil
}

// Origin returns the entry point that requested the batch.
func (s BatchSpec) Origin() types.BatchOrigin { return s.origin }

// Schedule returns the schedule that produced the batch, empty for non-scheduled work.
func (s BatchSpec) Schedule() string { return s.schedule }

// TaskSpec describes one task row an enqueue creates. The dedup key is not part of it: the task type
// definition derives the key from Payload, so a task cannot be created with a key its type does not
// own. An empty TaskID asks the enqueue to generate one; an explicit id keeps linked waiting children
// addressable. An empty Status means pending, and waiting creates a pre-linked child that activates
// when its parent completes; ParentTaskID names that parent and must refer to an earlier spec in the
// same enqueue, so a child is skipped together with a dedup-skipped parent.
type TaskSpec struct {
	Type         types.TaskType
	TaskID       string
	ParentTaskID string
	Payload      json.RawMessage
	Status       types.TaskStatus
}

// EnqueueResult reports what one enqueue created. BatchID is empty when nothing at all was created,
// which happens when every task lost a dedup conflict or hung off one that did; callers must not treat
// that as a failure. Skipped counts the tasks not created: dedup-index rejections plus the children
// skipped because their parent lost a conflict.
type EnqueueResult struct {
	BatchID string
	TaskIDs []string
	Skipped int
}

// PreconditionError reports that a task type's environment requirement was not met at all, so the whole
// enqueue created nothing. TaskType identifies the definition whose precondition failed, and Err is
// the cause.
type PreconditionError struct {
	TaskType types.TaskType
	Err      error
}

func (e *PreconditionError) Error() string {
	return fmt.Sprintf("precondition failed for %q: %v", e.TaskType, e.Err)
}

func (e *PreconditionError) Unwrap() error { return e.Err }

// Enqueuer is the single creation path for queued work: it turns a batch spec and all its task specs
// into one atomic batch, deriving each task's dedup key from its own type definition. Only entry-point
// boundaries construct a BatchSpec; services receive one as an argument.
type Enqueuer struct {
	client  *database.Client
	catalog TypeCatalog
}

// NewEnqueuer wires the enqueue to the database client and to the task type catalog the boundary constructed
// for its own enqueue surface.
func NewEnqueuer(client *database.Client, catalog TypeCatalog) *Enqueuer {
	return &Enqueuer{client: client, catalog: catalog}
}

// EnqueueBatch creates a batch and its tasks in one transaction: nothing is visible until commit, so
// a batch can never exist without tasks and its tasks can never exist without their batch. The origin
// and schedule come from spec, which is validated here and by its own constructor. The dedup keys are
// derived per task type for pending tasks only: waiting children carry none until activation, so any
// duplicate enqueued while they wait cannot collide with them. A task whose key already covers live
// work is skipped, and when every task is skipped nothing at all is created and the result then carries
// an empty BatchID. A pending task whose type has no dedup hook in this process's catalog is refused
// instead of being created without deduplication. The task types in one batch must declare the same
// Holds; their Preconditions run before any row is written and a failure creates nothing.
func (e *Enqueuer) EnqueueBatch(ctx context.Context, spec BatchSpec, tasks []TaskSpec) (EnqueueResult, error) {
	if err := spec.validate(); err != nil {
		return EnqueueResult{}, err
	}
	if len(tasks) == 0 {
		return EnqueueResult{}, nil
	}

	definitions := make([]TypeDefinition, len(tasks))
	statuses := make([]types.TaskStatus, len(tasks))
	for i, t := range tasks {
		def, err := e.catalog.Definition(t.Type)
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("resolve task type %q: %w", t.Type, err)
		}
		definitions[i] = def

		status, err := taskCreationStatus(t)
		if err != nil {
			return EnqueueResult{}, err
		}
		statuses[i] = status
		if status != types.Task.Status.Waiting && def.DedupKey == nil {
			return EnqueueResult{}, fmt.Errorf("%s task carries no dedup hook in this process", t.Type)
		}
		if t.ParentTaskID != "" && status != types.Task.Status.Waiting {
			return EnqueueResult{}, fmt.Errorf("%s task carries a parent link outside waiting status", t.Type)
		}
	}

	if err := validateParentLinks(tasks); err != nil {
		return EnqueueResult{}, err
	}

	if err := validateCommonHolds(definitions); err != nil {
		return EnqueueResult{}, err
	}
	if err := checkPreconditions(ctx, definitions); err != nil {
		return EnqueueResult{}, err
	}

	batchID := uuid.New().String()
	tx, err := e.client.BeginTx(ctx, nil)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin enqueue transaction: %w", err)
	}
	defer tx.Rollback()

	queries := e.client.Queries.WithTx(tx)

	var schedule sql.NullString
	if spec.schedule != "" {
		schedule = sql.NullString{String: spec.schedule, Valid: true}
	}
	if err := queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:       batchID,
		Origin:   spec.origin,
		Schedule: schedule,
		Status:   types.Batch.Status.Queued,
	}); err != nil {
		return EnqueueResult{}, fmt.Errorf("create batch: %w", err)
	}

	result := EnqueueResult{BatchID: batchID}
	created := make(map[string]struct{}, len(tasks))
	for i, t := range tasks {
		taskID := t.TaskID
		if taskID == "" {
			taskID = uuid.New().String()
		}

		if t.ParentTaskID != "" {
			if _, ok := created[t.ParentTaskID]; !ok {
				result.Skipped++
				continue
			}
		}

		var dedupKey sql.NullString
		// waiting children carry no key until activation: the partial dedup index only covers pending and
		// processing statuses, so a key while waiting protects nothing, and writing it at activation time
		// can collide with a duplicate the window allowed, stranding or failing the parent's child
		if statuses[i] != types.Task.Status.Waiting && definitions[i].DedupKey != nil {
			if key := definitions[i].DedupKey(t.Payload); key != "" {
				dedupKey = sql.NullString{String: key, Valid: true}
			}
		}

		payload := t.Payload
		// a dedup conflict must skip this task without aborting the batch, so an insert that carries a key
		// runs under its own savepoint: a violation of the dedup index rolls back to that savepoint, and the
		// transaction stays usable for the remaining tasks; a collision on another unique constraint, the task
		// id above all, is not a dedup conflict and aborts, while a task without a key cannot violate the
		// partial dedup index at all and inserts directly
		if dedupKey.Valid {
			if _, err := tx.ExecContext(ctx, "SAVEPOINT task_insert"); err != nil {
				return EnqueueResult{}, fmt.Errorf("set insert savepoint: %w", err)
			}
		}
		if _, err := queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   taskID,
			TaskType: t.Type,
			Status:   statuses[i],
			BatchID:  sql.NullString{String: batchID, Valid: true},
			Payload:  &payload,
			DedupKey: dedupKey,
		}); err != nil {
			if dedupKey.Valid && errs.IsConstraintOn(err, dedupIndexName) {
				if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT task_insert"); rbErr != nil {
					return EnqueueResult{}, fmt.Errorf("roll back dedup savepoint: %w", rbErr)
				}
				result.Skipped++
				continue
			}
			return EnqueueResult{}, fmt.Errorf("create %s task: %w", t.Type, err)
		}
		if dedupKey.Valid {
			if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT task_insert"); err != nil {
				return EnqueueResult{}, fmt.Errorf("release insert savepoint: %w", err)
			}
		}
		created[taskID] = struct{}{}
		result.TaskIDs = append(result.TaskIDs, taskID)
	}

	if len(result.TaskIDs) == 0 {
		return EnqueueResult{Skipped: result.Skipped}, nil
	}

	if err := tx.Commit(); err != nil {
		return EnqueueResult{}, fmt.Errorf("commit enqueue transaction: %w", err)
	}
	return result, nil
}

// EnqueueTask enqueues a single task through EnqueueBatch, sharing its validation, dedup and atomicity.
func (e *Enqueuer) EnqueueTask(ctx context.Context, spec BatchSpec, t TaskSpec) (EnqueueResult, error) {
	return e.EnqueueBatch(ctx, spec, []TaskSpec{t})
}

// DedupKey derives the key a task of the given type would carry with the given payload. Boundaries use
// it to pre-check coverage with the same derivation the enqueue applies, instead of rebuilding the key
// vocabulary; a type without a hook in this process's catalog is an error, never an empty key.
func (e *Enqueuer) DedupKey(taskType types.TaskType, payload json.RawMessage) (string, error) {
	def, err := e.catalog.Definition(taskType)
	if err != nil {
		return "", fmt.Errorf("resolve task type %q: %w", taskType, err)
	}
	if def.DedupKey == nil {
		return "", fmt.Errorf("%s task carries no dedup hook in this process", taskType)
	}
	return def.DedupKey(payload), nil
}

// validateParentLinks enforces that a linked child's parent appears earlier in the same enqueue: the
// enqueue can only skip a child once it has already settled the parent's dedup outcome.
func validateParentLinks(tasks []TaskSpec) error {
	seen := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		if t.ParentTaskID != "" {
			if _, ok := seen[t.ParentTaskID]; !ok {
				return fmt.Errorf("%s task parent %q does not precede it in the same enqueue", t.Type, t.ParentTaskID)
			}
		}
		if t.TaskID != "" {
			seen[t.TaskID] = struct{}{}
		}
	}
	return nil
}

// taskCreationStatus normalizes a spec's status to pending and rejects every status other than pending
// or waiting. A waiting child must carry its explicit id, because its parent payload links to it.
func taskCreationStatus(t TaskSpec) (types.TaskStatus, error) {
	switch t.Status {
	case "":
		return types.Task.Status.Pending, nil
	case types.Task.Status.Pending:
		return types.Task.Status.Pending, nil
	case types.Task.Status.Waiting:
		if t.TaskID == "" {
			return "", fmt.Errorf("waiting %s task requires an explicit task id", t.Type)
		}
		return types.Task.Status.Waiting, nil
	default:
		return "", fmt.Errorf("task status %q cannot create work, only pending or waiting", t.Status)
	}
}

// validateCommonHolds enforces that every task type in a batch declares the same Holds set; WaitsFor
// may vary per task type.
func validateCommonHolds(definitions []TypeDefinition) error {
	first := definitions[0]
	firstKey := holdsKey(first.Locks.Holds)
	for _, def := range definitions[1:] {
		if holdsKey(def.Locks.Holds) != firstKey {
			return fmt.Errorf("task types %q and %q declare different holds in one batch", first.Type, def.Type)
		}
	}
	return nil
}

// holdsKey renders a holds declaration as an order-independent comparison key.
func holdsKey(holds []types.LockName) string {
	sorted := slices.Clone(holds)
	slices.Sort(sorted)
	return fmt.Sprint(sorted)
}

// checkPreconditions evaluates each distinct task type's preconditions once, before any row is written.
func checkPreconditions(ctx context.Context, definitions []TypeDefinition) error {
	checked := make(map[types.TaskType]struct{}, len(definitions))
	for _, def := range definitions {
		if _, ok := checked[def.Type]; ok {
			continue
		}
		checked[def.Type] = struct{}{}
		for _, precondition := range def.Preconditions {
			if err := precondition(ctx); err != nil {
				return &PreconditionError{TaskType: def.Type, Err: err}
			}
		}
	}
	return nil
}
