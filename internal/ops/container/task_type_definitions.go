package container

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

const (
	thumbnailBackfillPageSize = 500
	dailyDedupKeyDateLayout   = "2006-01-02"
)

// Catalog is the concrete task type table: one definition per task type, composing identity, queued
// locks, dedup derivation, preconditions and the typed-task-set builder. Handlers are not part of it;
// the handler registry keeps them.
type Catalog struct {
	definitions map[types.TaskType]task.TypeDefinition
}

// lockPolicy adapts a task type's closed lock declaration from the vocabulary to the task package's
// policy value, so the definitions and the claim paths cannot drift.
func lockPolicy(taskType types.TaskType) task.LockPolicy {
	declaration := types.LockDeclarationFor(taskType)
	return task.LockPolicy{Holds: declaration.Holds, WaitsFor: declaration.WaitsFor}
}

// NewCatalog builds the task type table; builders read the database through the queries it carries, the
// worker counts through the config accessor, and report skipped inputs through the logger, so a hot-reloaded
// configuration reaches the next batch execution without rebuilding the table.
func NewCatalog(queries *database.Queries, getCfg func() *config.Config, logger *log.Logger) *Catalog {
	return &Catalog{definitions: map[types.TaskType]task.TypeDefinition{
		types.Task.Type.Consume: {
			Type:          types.Task.Type.Consume,
			Locks:         lockPolicy(types.Task.Type.Consume),
			DirectLocks:   &task.LockPolicy{Holds: []types.LockName{types.LockMaintenance}},
			CLI:           "consume",
			BatchSelector: "--from-batch",
			Hosts:         []types.Host{types.HostQueue},
			Concurrency:   getCfg().Consumer.Workers,
			DedupKey:      consumeDedupKey,
			BuildTasks:    buildIngestTasks(getCfg, logger),
		},
		types.Task.Type.Enrich: {
			Type:          types.Task.Type.Enrich,
			Locks:         lockPolicy(types.Task.Type.Enrich),
			DirectLocks:   &task.LockPolicy{Holds: []types.LockName{types.LockMaintenance}},
			CLI:           "enrich",
			BatchSelector: "--from-batch",
			Hosts:         []types.Host{types.HostQueue},
			Concurrency:   getCfg().Enricher.Workers,
			DedupKey:      enrichDedupKey,
			BuildTasks:    buildEnrichTasks,
		},
		types.Task.Type.Thumbnail: {
			Type:          types.Task.Type.Thumbnail,
			Locks:         lockPolicy(types.Task.Type.Thumbnail),
			DirectLocks:   &task.LockPolicy{Holds: []types.LockName{types.LockStorage}, WaitsFor: []types.LockName{types.LockMaintenance}},
			CLI:           "thumbnails",
			BatchSelector: "--from-batch",
			Hosts:         []types.Host{types.HostQueue},
			Concurrency:   getCfg().Consumer.Thumbnail.Workers,
			DedupKey:      thumbnailDedupKey,
		},
		types.Task.Type.ThumbBackfill: {
			Type:          types.Task.Type.ThumbBackfill,
			Locks:         lockPolicy(types.Task.Type.ThumbBackfill),
			CLI:           "thumbnails",
			BatchSelector: "--from-batch",
			Hosts:         []types.Host{types.HostQueue},
			DedupKey:      thumbnailDedupKey,
			BuildTasks:    buildThumbnailBackfillTasks(queries),
		},
		types.Task.Type.Backup: {
			Type:       types.Task.Type.Backup,
			Locks:      lockPolicy(types.Task.Type.Backup),
			DedupKey:   backupDedupKey,
			BuildTasks: buildBackupTasks,
		},
		types.Task.Type.Mirror: {
			Type:       types.Task.Type.Mirror,
			Locks:      lockPolicy(types.Task.Type.Mirror),
			DedupKey:   mirrorDedupKey,
			BuildTasks: buildMirrorTasks,
		},
		types.Task.Type.TagReconcile: {
			Type:       types.Task.Type.TagReconcile,
			Locks:      lockPolicy(types.Task.Type.TagReconcile),
			DedupKey:   constantDedupKey(curation.TagReconcileDedupKey),
			BuildTasks: buildTagTask(types.Task.Type.TagReconcile),
		},
		types.Task.Type.TagMeasure: {
			Type:       types.Task.Type.TagMeasure,
			Locks:      lockPolicy(types.Task.Type.TagMeasure),
			DedupKey:   constantDedupKey(tags.TagMeasureDedupKey),
			BuildTasks: buildTagTask(types.Task.Type.TagMeasure),
		},
		types.Task.Type.TagIdentity: {
			Type:       types.Task.Type.TagIdentity,
			Locks:      lockPolicy(types.Task.Type.TagIdentity),
			DedupKey:   constantDedupKey(curation.TagIdentityDedupKey),
			BuildTasks: buildTagTask(types.Task.Type.TagIdentity),
		},
		types.Task.Type.TagAudit: {
			Type:       types.Task.Type.TagAudit,
			Locks:      lockPolicy(types.Task.Type.TagAudit),
			DedupKey:   constantDedupKey(curation.TagAuditDedupKey),
			BuildTasks: buildTagTask(types.Task.Type.TagAudit),
		},
		types.Task.Type.MigrateDB: {
			Type:     types.Task.Type.MigrateDB,
			Locks:    lockPolicy(types.Task.Type.MigrateDB),
			DedupKey: constantDedupKey(configtask.DedupKeyMigrateDB),
		},
		types.Task.Type.MigrateStorage: {
			Type:     types.Task.Type.MigrateStorage,
			Locks:    lockPolicy(types.Task.Type.MigrateStorage),
			DedupKey: constantDedupKey(configtask.DedupKeyMigrateStorage),
		},
		types.Task.Type.Config: {
			Type:     types.Task.Type.Config,
			Locks:    lockPolicy(types.Task.Type.Config),
			DedupKey: configDedupKey,
		},
	}}
}

// Definition returns the entry for a task type; an unknown type is an error.
func (c *Catalog) Definition(taskType types.TaskType) (task.TypeDefinition, error) {
	def, ok := c.definitions[taskType]
	if !ok {
		return task.TypeDefinition{}, fmt.Errorf("unknown task type: %q", taskType)
	}
	return def, nil
}

// EmptyBuildInput is the builder input for task types whose enqueue takes no parameters at all. Its
// task type stays explicit so a builder can verify the input belongs to it before using it.
type EmptyBuildInput struct {
	taskType types.TaskType
}

// NewEmptyBuildInput returns the parameterless input of one task type.
func NewEmptyBuildInput(taskType types.TaskType) EmptyBuildInput {
	return EmptyBuildInput{taskType: taskType}
}

// TaskType reports the task type the input addresses.
func (i EmptyBuildInput) TaskType() types.TaskType { return i.taskType }

// BackupBuildInput is the backup builder input: the mode, the destination override and the retention
// count, plus the schedule that triggered the run.
type BackupBuildInput struct {
	Mode     string
	Path     string
	Keep     int
	Schedule backup.ScheduleTiming
}

// TaskType reports the task type the input addresses.
func (BackupBuildInput) TaskType() types.TaskType { return types.Task.Type.Backup }

// MirrorBuildInput is the mirror builder input: the destination override, the configured interval and
// the schedule that triggered the run.
type MirrorBuildInput struct {
	Path     string
	Interval float64
	Schedule backup.ScheduleTiming
}

// TaskType reports the task type the input addresses.
func (MirrorBuildInput) TaskType() types.TaskType { return types.Task.Type.Mirror }

// EnrichBuildInput is the re-enrichment builder input: the documents whose enrichment is queued in a
// single batch.
type EnrichBuildInput struct {
	DocumentIDs []string
}

// TaskType reports the task type the input addresses.
func (EnrichBuildInput) TaskType() types.TaskType { return types.Task.Type.Enrich }

// requireTaskType rejects an input built for another task type before the builder asserts its concrete
// struct.
func requireTaskType(input task.BuildInput, want types.TaskType) error {
	if input == nil {
		return fmt.Errorf("%s builder received no input", want)
	}
	if got := input.TaskType(); got != want {
		return fmt.Errorf("%s builder received an input for %s", want, got)
	}
	return nil
}

func buildBackupTasks(_ context.Context, input task.BuildInput) ([]task.TaskSpec, error) {
	if err := requireTaskType(input, types.Task.Type.Backup); err != nil {
		return nil, err
	}
	in, ok := input.(BackupBuildInput)
	if !ok {
		return nil, fmt.Errorf("backup builder received %T", input)
	}
	payload, err := json.Marshal(backup.BackupPayload{
		Mode:     in.Mode,
		Path:     in.Path,
		Keep:     in.Keep,
		Schedule: in.Schedule,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal backup payload: %w", err)
	}
	return []task.TaskSpec{{Type: types.Task.Type.Backup, Payload: payload}}, nil
}

func buildMirrorTasks(_ context.Context, input task.BuildInput) ([]task.TaskSpec, error) {
	if err := requireTaskType(input, types.Task.Type.Mirror); err != nil {
		return nil, err
	}
	in, ok := input.(MirrorBuildInput)
	if !ok {
		return nil, fmt.Errorf("mirror builder received %T", input)
	}
	payload, err := json.Marshal(backup.MirrorPayload{
		Path:     in.Path,
		Interval: in.Interval,
		Schedule: in.Schedule,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mirror payload: %w", err)
	}
	return []task.TaskSpec{{Type: types.Task.Type.Mirror, Payload: payload}}, nil
}

func buildEnrichTasks(_ context.Context, input task.BuildInput) ([]task.TaskSpec, error) {
	if err := requireTaskType(input, types.Task.Type.Enrich); err != nil {
		return nil, err
	}
	in, ok := input.(EnrichBuildInput)
	if !ok {
		return nil, fmt.Errorf("enrich builder received %T", input)
	}
	specs := make([]task.TaskSpec, 0, len(in.DocumentIDs))
	for _, documentID := range in.DocumentIDs {
		payload, err := json.Marshal(map[string]string{"document_id": documentID})
		if err != nil {
			return nil, fmt.Errorf("marshal enrich payload: %w", err)
		}
		specs = append(specs, task.TaskSpec{Type: types.Task.Type.Enrich, Payload: payload})
	}
	return specs, nil
}

// buildIngestTasks scans the inbox and returns one consume task plus its pre-linked waiting children per
// file, the same task set the polling scan uses. A file whose checksum cannot be read is reported and
// skipped, matching the polling and direct paths: one unreadable file must not block the whole enqueue.
// Dedup handles files already covered by live work, so a rescan skips them at enqueue.
func buildIngestTasks(getCfg func() *config.Config, logger *log.Logger) func(context.Context, task.BuildInput) ([]task.TaskSpec, error) {
	return func(_ context.Context, input task.BuildInput) ([]task.TaskSpec, error) {
		if err := requireTaskType(input, types.Task.Type.Consume); err != nil {
			return nil, err
		}
		cfg := getCfg()
		files, err := consumption.GetFiles(cfg.Storage.ConsumptionDir, cfg.Consumer.SupportedFiles, cfg.Consumer.MaxFilesPerBatch)
		if err != nil {
			return nil, fmt.Errorf("scan inbox: %w", err)
		}
		specs := make([]task.TaskSpec, 0, len(files)*2)
		for i, file := range files {
			md5hash, err := storage.CalculateMD5(file.OriginalPath)
			if err != nil {
				logger.Error("", "batch create consume: md5 %s: %v", file.OriginalPath, err)
				continue
			}
			specs = append(specs, consumption.IngestTaskSpecs(consumption.IngestFile{
				Path:       file.OriginalPath,
				Index:      i + 1,
				MD5:        md5hash,
				DocumentID: uuid.New().String(),
			}, cfg.Consumer.Thumbnail.Enabled)...)
		}
		return specs, nil
	}
}

func buildThumbnailBackfillTasks(queries *database.Queries) func(context.Context, task.BuildInput) ([]task.TaskSpec, error) {
	return func(ctx context.Context, input task.BuildInput) ([]task.TaskSpec, error) {
		if err := requireTaskType(input, types.Task.Type.ThumbBackfill); err != nil {
			return nil, err
		}
		var specs []task.TaskSpec
		var lastID int64
		for {
			rows, err := queries.ListDocumentsWithoutThumbnails(ctx, database.ListDocumentsWithoutThumbnailsParams{
				ID:    lastID,
				Limit: thumbnailBackfillPageSize,
			})
			if err != nil {
				return nil, fmt.Errorf("list documents without thumbnails: %w", err)
			}
			if len(rows) == 0 {
				return specs, nil
			}
			for _, row := range rows {
				lastID = row.ID
				payload, err := json.Marshal(map[string]string{
					"document_id":  row.DocumentID,
					"storage_path": row.StoragePath,
				})
				if err != nil {
					return nil, fmt.Errorf("marshal thumbnail payload: %w", err)
				}
				specs = append(specs, task.TaskSpec{
					Type:    types.Task.Type.ThumbBackfill,
					Payload: payload,
				})
			}
		}
	}
}

func buildTagTask(taskType types.TaskType) func(context.Context, task.BuildInput) ([]task.TaskSpec, error) {
	return func(_ context.Context, input task.BuildInput) ([]task.TaskSpec, error) {
		if err := requireTaskType(input, taskType); err != nil {
			return nil, err
		}
		return []task.TaskSpec{{Type: taskType, Payload: json.RawMessage("{}")}}, nil
	}
}

func constantDedupKey(key string) func(json.RawMessage) string {
	return func(json.RawMessage) string { return key }
}

func consumeDedupKey(payload json.RawMessage) string {
	var p struct {
		MD5 string `json:"md5"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.MD5 == "" {
		return ""
	}
	return "consume:" + p.MD5
}

func enrichDedupKey(payload json.RawMessage) string {
	var p struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.DocumentID == "" {
		return ""
	}
	return "enrich:doc:" + p.DocumentID
}

func thumbnailDedupKey(payload json.RawMessage) string {
	var p struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.DocumentID == "" {
		return ""
	}
	return "thumbnail:doc:" + p.DocumentID
}

func backupDedupKey(payload json.RawMessage) string {
	var p struct {
		Mode string `json:"mode"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	if p.Mode == "" {
		p.Mode = string(backup.BackupModeFull)
	}
	return fmt.Sprintf("backup:%s:%s", p.Mode, time.Now().UTC().Format(dailyDedupKeyDateLayout))
}

func mirrorDedupKey(json.RawMessage) string {
	return fmt.Sprintf("mirror:%s", time.Now().UTC().Format(dailyDedupKeyDateLayout))
}

func configDedupKey(payload json.RawMessage) string {
	var p struct {
		Op   string `json:"op"`
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	switch p.Op {
	case configtask.OpTessdata:
		return configtask.DedupKeyTessdataPrefix + p.Lang
	case configtask.OpHugot:
		return configtask.DedupKeyHugot
	default:
		return ""
	}
}
