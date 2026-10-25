package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// testChildExitEnv makes this test binary a deterministic launcher child: the dispatcher and
// LaunchQueuedBatch fork os.Args[0], and an inherited value turns that fork into an immediate
// non-zero exit instead of a recursive run of the whole suite.
const testChildExitEnv = "KUSHIM_QUEUE_TEST_CHILD"

// exit code the launch-failure tests use; never a success code
const testChildExitCode = "9"

func TestMain(m *testing.M) {
	if code := os.Getenv(testChildExitEnv); code != "" {
		exit, err := strconv.Atoi(code)
		if err != nil {
			exit = 1
		}
		os.Exit(exit)
	}
	os.Exit(m.Run())
}

func setupLauncherTest(t *testing.T) (*database.Client, *config.Config) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	cfg, cleanupCfg := testutil.NewTestConfig(t)
	t.Cleanup(cleanupCfg)
	// the dispatch path builds the task catalog, whose consumer construction verifies the
	// converter binary when enabled; these tests never convert a document, so the gate stays
	// off instead of depending on a LibreOffice install
	cfg.Consumer.Converter.Enabled = false
	return client, cfg
}

func launcherContainer(t *testing.T, client *database.Client, cfg *config.Config) *container.Container {
	t.Helper()
	c := container.NewContainerWithDB(cfg, testutil.NewTestLogger(), client.DB())
	t.Cleanup(func() { c.Close() })
	return c
}

// seedBatch creates a queued batch with one task of the given type.
func seedBatch(t *testing.T, client *database.Client, batchID string, taskType types.TaskType) {
	t.Helper()
	ctx := context.Background()
	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: batchID, Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Queued,
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	payload := json.RawMessage(`{}`)
	if _, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID: batchID + "-t", TaskType: taskType, Status: types.Task.Status.Pending,
		Payload: &payload, BatchID: sql.NullString{String: batchID, Valid: true},
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
}

// seedClaimedBatch creates a processing batch whose lease belongs to owner under pid.
func seedClaimedBatch(t *testing.T, client *database.Client, batchID, owner string, pid int64, taskType types.TaskType) {
	t.Helper()
	seedBatch(t, client, batchID, taskType)
	ctx := context.Background()
	if _, err := client.Queries.MarkBatchClaimed(ctx, batchID); err != nil {
		t.Fatalf("mark claimed: %v", err)
	}
	if _, err := client.Queries.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
		BatchID: batchID, OwnerID: owner, Pid: pid,
	}); err != nil {
		t.Fatalf("insert lease: %v", err)
	}
}

type stubCatalog map[types.TaskType]task.TypeDefinition

func (s stubCatalog) Definition(taskType types.TaskType) (task.TypeDefinition, error) {
	def, ok := s[taskType]
	if !ok {
		return task.TypeDefinition{}, fmt.Errorf("unknown task type %s", taskType)
	}
	return def, nil
}

// TestResolveLaunchTarget pins the dispatcher's executor resolution: every task type must be
// dispatchable on the host, the types must agree on one command, and a type without a batch-mode
// CLI keeps its batches off this dispatcher.
func TestResolveLaunchTarget(t *testing.T) {
	batchID := "rlt-batch"
	consume := task.TypeDefinition{CLI: "consume", BatchSelector: "--from-batch", Hosts: []types.Host{types.HostQueue}}
	enrich := task.TypeDefinition{CLI: "enrich", BatchSelector: "--from-batch", Hosts: []types.Host{types.HostQueue}}
	thumbnails := task.TypeDefinition{CLI: "thumbnails", BatchSelector: "--from-batch", Hosts: []types.Host{types.HostQueue}}
	catalog := stubCatalog{
		types.Task.Type.Consume:       consume,
		types.Task.Type.Enrich:        enrich,
		types.Task.Type.Thumbnail:     thumbnails,
		types.Task.Type.ThumbBackfill: thumbnails,
		types.Task.Type.Backup:        {Type: types.Task.Type.Backup},
		types.Task.Type.TagAudit: {
			CLI: "kushim-tag-audit-not-on-this-host", BatchSelector: "--from-batch",
			Hosts: []types.Host{types.HostEdub},
		},
		types.Task.Type.TagMeasure: {
			CLI: "tagmeasure", BatchSelector: "--run", Hosts: []types.Host{types.HostQueue},
		},
		types.Task.Type.TagIdentity: {
			CLI: "tagidentity", BatchSelector: "--run", Hosts: []types.Host{types.HostQueue},
		},
	}

	t.Run("a pure batch resolves to its own command", func(t *testing.T) {
		target, err := resolveLaunchTarget(catalog, batchID,
			[]types.TaskType{types.Task.Type.Enrich}, types.HostQueue)
		testutil.AssertNoError(t, err, "resolve a pure enrich batch")
		testutil.AssertEqual(t, target.batchID, batchID, "batch id")
		testutil.AssertEqual(t, target.cli, "enrich", "the type's own command")
		testutil.AssertEqual(t, target.selector, "--from-batch", "the batch selector")
	})

	t.Run("a mixed ingest batch resolves to the owning command", func(t *testing.T) {
		target, err := resolveLaunchTarget(catalog, batchID,
			[]types.TaskType{types.Task.Type.Consume, types.Task.Type.Enrich, types.Task.Type.Thumbnail},
			types.HostQueue)
		testutil.AssertNoError(t, err, "resolve a mixed ingest batch")
		testutil.AssertEqual(t, target.cli, "consume", "the consume type owns the batch")
		testutil.AssertEqual(t, target.selector, "--from-batch", "the batch selector")
	})

	t.Run("sibling types naming the same command resolve it", func(t *testing.T) {
		target, err := resolveLaunchTarget(catalog, batchID,
			[]types.TaskType{types.Task.Type.Thumbnail, types.Task.Type.ThumbBackfill}, types.HostQueue)
		testutil.AssertNoError(t, err, "resolve a thumbnail-family batch")
		testutil.AssertEqual(t, target.cli, "thumbnails", "the shared command")
	})

	t.Run("a batch with no task types is refused", func(t *testing.T) {
		if _, err := resolveLaunchTarget(catalog, batchID, nil, types.HostQueue); err == nil {
			t.Fatal("empty type set must be refused")
		}
	})

	t.Run("a type without a batch-mode CLI is refused", func(t *testing.T) {
		_, err := resolveLaunchTarget(catalog, batchID, []types.TaskType{types.Task.Type.Backup}, types.HostQueue)
		if err == nil || !strings.Contains(err.Error(), "no batch-mode CLI") {
			t.Fatalf("want the missing-CLI refusal, got %v", err)
		}
	})

	t.Run("a type whose host list excludes this host is refused", func(t *testing.T) {
		_, err := resolveLaunchTarget(catalog, batchID, []types.TaskType{types.Task.Type.TagAudit}, types.HostQueue)
		if err == nil || !strings.Contains(err.Error(), "not dispatchable on host") {
			t.Fatalf("want the host refusal, got %v", err)
		}
	})

	t.Run("types outside the precedence list that disagree fail loudly", func(t *testing.T) {
		_, err := resolveLaunchTarget(catalog, batchID,
			[]types.TaskType{types.Task.Type.TagMeasure, types.Task.Type.TagIdentity}, types.HostQueue)
		if err == nil || !strings.Contains(err.Error(), "disagree") {
			t.Fatalf("want the disagreement refusal, got %v", err)
		}
	})
}

// TestBatchLockSets pins the claim-side lock rule: only the holds of holding types, and only the
// waits those holding types declare.
func TestBatchLockSets(t *testing.T) {
	cases := []struct {
		name      string
		taskTypes []types.TaskType
		holds     []types.LockName
		waits     []types.LockName
	}{
		{
			name:      "an ingest batch holds and waits for nothing",
			taskTypes: []types.TaskType{types.Task.Type.Consume, types.Task.Type.Enrich, types.Task.Type.Thumbnail},
		},
		{
			name:      "a thumbbackfill batch holds storage and waits for maintenance",
			taskTypes: []types.TaskType{types.Task.Type.ThumbBackfill},
			holds:     []types.LockName{types.LockStorage},
			waits:     []types.LockName{types.LockMaintenance},
		},
		{
			name: "a holding type's waits count, a non-holding type's do not",
			taskTypes: []types.TaskType{
				types.Task.Type.Consume, types.Task.Type.Thumbnail, types.Task.Type.ThumbBackfill,
			},
			holds: []types.LockName{types.LockStorage},
			waits: []types.LockName{types.LockMaintenance},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			holds, waits := batchLockSets(tc.taskTypes)
			testutil.AssertEqual(t, len(holds), len(tc.holds), "holds count")
			testutil.AssertEqual(t, len(waits), len(tc.waits), "waits count")
			for _, lockName := range tc.holds {
				if !slices.Contains(holds, lockName) {
					t.Fatalf("holds %v missing %s", holds, lockName)
				}
			}
			for _, lockName := range tc.waits {
				if !slices.Contains(waits, lockName) {
					t.Fatalf("waits %v missing %s", waits, lockName)
				}
			}
		})
	}
}

// TestLaunchBackoff pins the bounded retry policy: consecutive failures cool the batch down with
// growing delays, the failure that reaches maxLaunchFailures forgets the batch so it can be failed
// instead of retried, and a launch that ran clears the streak.
func TestLaunchBackoff(t *testing.T) {
	b := newLaunchBackoff()
	now := time.Now()

	if b.cooling("b1", now) {
		t.Fatal("a batch without failures must not be cooling")
	}

	count, delay := b.record("b1")
	if count != 1 || delay != 5*time.Second {
		t.Fatalf("first failure = (%d, %s), want (1, 5s)", count, delay)
	}
	if !b.cooling("b1", now.Add(2*time.Second)) {
		t.Fatal("inside the first window the batch must cool")
	}
	if b.cooling("b1", now.Add(6*time.Second)) {
		t.Fatal("after the first window the batch must be claimable again")
	}

	count, delay = b.record("b1")
	if count != 2 || delay != 10*time.Second {
		t.Fatalf("second failure = (%d, %s), want (2, 10s)", count, delay)
	}

	count, delay = b.record("b1")
	if count != maxLaunchFailures || delay != 0 {
		t.Fatalf("third failure = (%d, %s), want (%d, 0) so the batch is failed not retried",
			count, delay, maxLaunchFailures)
	}
	if b.cooling("b1", now.Add(time.Hour)) {
		t.Fatal("a failed batch must not stay in the backoff map")
	}

	b.record("b2")
	b.clear("b2")
	if b.cooling("b2", time.Now()) {
		t.Fatal("clear must forget the streak")
	}
}

func TestRequeueAfterLaunchFailure(t *testing.T) {
	client, _ := setupLauncherTest(t)
	ctx := context.Background()
	logger := testutil.NewTestLogger()

	t.Run("releases the lease and hands the batch back to the dispatcher", func(t *testing.T) {
		token := uuid.New()
		seedClaimedBatch(t, client, "requeue-ours", token.String(), 0, types.Task.Type.Consume)
		requeueAfterLaunchFailure(ctx, client, logger, "requeue-ours", token)

		batch, err := client.Queries.GetBatch(ctx, "requeue-ours")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Queued, "handed back as queued")
		if _, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
			BatchID: "requeue-ours", StaleAfterSeconds: task.LeaseStaleSeconds,
		}); err == nil {
			t.Fatal("the failing execution's lease must be released")
		}
	})

	t.Run("a batch that changed hands is left alone", func(t *testing.T) {
		foreign := uuid.New()
		seedClaimedBatch(t, client, "requeue-foreign", foreign.String(), 77, types.Task.Type.Consume)
		requeueAfterLaunchFailure(ctx, client, logger, "requeue-foreign", uuid.New())

		batch, err := client.Queries.GetBatch(ctx, "requeue-foreign")
		testutil.AssertNoError(t, err, "read batch")
		testutil.AssertEqual(t, batch.Status, types.Batch.Status.Processing, "foreign-owned batch untouched")
		owner, err := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
			BatchID: "requeue-foreign", StaleAfterSeconds: task.LeaseStaleSeconds,
		})
		testutil.AssertNoError(t, err, "foreign lease still present")
		testutil.AssertEqual(t, owner.OwnerID, foreign.String(), "lease owner unchanged")
	})
}

// TestLaunchQueuedBatchRefusals pins `kushim batch run`'s refusal matrix: every batch state that
// cannot be claimed right now gets an error that names the state and what recovers it, and nothing is
// forked.
func TestLaunchQueuedBatchRefusals(t *testing.T) {
	client, cfg := setupLauncherTest(t)
	c := launcherContainer(t, client, cfg)
	ctx := context.Background()

	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: "run-done", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Completed,
	}); err != nil {
		t.Fatalf("create completed batch: %v", err)
	}
	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: "run-paused", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Paused,
	}); err != nil {
		t.Fatalf("create paused batch: %v", err)
	}
	seedClaimedBatch(t, client, "run-live", uuid.New().String(), int64(os.Getpid()), types.Task.Type.Consume)
	seedClaimedBatch(t, client, "run-dead", uuid.New().String(), 999999999, types.Task.Type.Consume)
	seedClaimedBatch(t, client, "run-preadopt", uuid.New().String(), 0, types.Task.Type.Consume)
	seedBatch(t, client, "run-backup", types.Task.Type.Backup)
	seedClaimedBatch(t, client, "run-stale", uuid.New().String(), 77, types.Task.Type.Consume)
	if _, err := client.DB().ExecContext(ctx,
		"UPDATE batch_owner SET last_heartbeat = NOW() - INTERVAL '5 minutes' WHERE batch_id = $1",
		"run-stale"); err != nil {
		t.Fatalf("stale the lease: %v", err)
	}
	seedBatch(t, client, "run-orphan", types.Task.Type.Consume)
	if _, err := client.Queries.MarkBatchClaimed(ctx, "run-orphan"); err != nil {
		t.Fatalf("leave a processing batch without a lease: %v", err)
	}
	seedBatch(t, client, "run-pool", types.Task.Type.Backup)
	if _, err := client.Queries.MarkBatchClaimed(ctx, "run-pool"); err != nil {
		t.Fatalf("leave a pool batch processing: %v", err)
	}

	cases := []struct {
		batchID string
		want    string
	}{
		{batchID: "run-missing", want: "not found"},
		{batchID: "run-done", want: "cannot run"},
		{batchID: "run-paused", want: "paused"},
		{batchID: "run-live", want: fmt.Sprintf("being processed by PID %d", os.Getpid())},
		{batchID: "run-dead", want: "which is no longer running"},
		{batchID: "run-preadopt", want: "child has not adopted it yet"},
		{batchID: "run-stale", want: "processing under a stale lease"},
		{batchID: "run-orphan", want: "processing without a lease"},
		{batchID: "run-pool", want: "processing without a lease"},
		{batchID: "run-backup", want: "no batch-mode CLI"},
	}
	for _, tc := range cases {
		t.Run(tc.batchID, func(t *testing.T) {
			err := LaunchQueuedBatch(ctx, c, tc.batchID)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LaunchQueuedBatch(%s) = %v, want an error mentioning %q", tc.batchID, err, tc.want)
			}
		})
	}

	batch, err := client.Queries.GetBatch(ctx, "run-backup")
	testutil.AssertNoError(t, err, "read backup batch")
	testutil.AssertEqual(t, batch.Status, types.Batch.Status.Queued, "a refused run must not claim the batch")
}

// TestLaunchQueuedBatchClaimsAndForks pins the manual launcher's happy path: it claims the queued
// batch, forks the task CLI with the execution token and surfaces a non-zero child exit.
func TestLaunchQueuedBatchClaimsAndForks(t *testing.T) {
	client, cfg := setupLauncherTest(t)
	c := launcherContainer(t, client, cfg)
	ctx := context.Background()
	t.Setenv(testChildExitEnv, testChildExitCode)

	seedBatch(t, client, "run-fork", types.Task.Type.Consume)
	err := LaunchQueuedBatch(ctx, c, "run-fork")
	if err == nil || !strings.Contains(err.Error(), "exited with error") {
		t.Fatalf("want the child's non-zero exit surfaced, got %v", err)
	}

	batch, fetchErr := client.Queries.GetBatch(ctx, "run-fork")
	testutil.AssertNoError(t, fetchErr, "read batch")
	testutil.AssertEqual(t, batch.Status, types.Batch.Status.Processing, "the batch was claimed before the fork")
	owner, ownerErr := client.Queries.GetBatchOwner(ctx, database.GetBatchOwnerParams{
		BatchID: "run-fork", StaleAfterSeconds: task.LeaseStaleSeconds,
	})
	testutil.AssertNoError(t, ownerErr, "lease present after the failed run")
	testutil.AssertEqual(t, owner.Pid, int64(0), "the child never adopted; the reaper owns recovery")
}
