package consumption

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func ScanAndEnqueue(ctx context.Context, cfg *config.Config, client *database.Client, logger *utils.Logger) (batchID string, enqueued int, err error) {
	pausedCount, err := client.Queries.CountPausedBatches(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("count paused batches: %w", err)
	}
	if pausedCount > 0 {
		logger.Info(nil, "scan: skipped — %d paused batch(es) exist; resolve credit issue first", pausedCount)
		return "", 0, nil
	}

	paths, err := utils.ListFilePaths(
		cfg.Storage.ConsumptionDir,
		cfg.Consumer.SupportedFiles,
		cfg.Consumer.MaxFilesPerBatch,
	)
	if err != nil {
		return "", 0, err
	}

	if len(paths) == 0 {
		return "", 0, nil
	}

	batchID = uuid.New().String()

	// Compute MD5 for all files, skipping unreadable ones.
	type fileEntry struct {
		path  string
		md5   string
		index int
	}
	var entries []fileEntry
	for i, path := range paths {
		md5hash, md5Err := utils.CalculateMD5(path)
		if md5Err != nil {
			logger.Error(nil, "scan: md5 %s: %v", path, md5Err)
			continue
		}
		entries = append(entries, fileEntry{path: path, md5: md5hash, index: i + 1})
	}

	if len(entries) == 0 {
		return "", 0, nil
	}

	// Batch dedup: single query for all MD5 hashes instead of one per file.
	md5hashes := make([]string, len(entries))
	for i, e := range entries {
		md5hashes[i] = e.md5
	}
	duplicates, dupErr := queryDuplicatesByMD5(ctx, client, md5hashes)
	if dupErr != nil {
		logger.Error(nil, "scan: batch dedup query: %v", dupErr)
		duplicates = make(map[string]duplicateInfo)
	}

	// MD5s already enqueued earlier in this scan; the consume task's dedup
	// key ("consume:<md5>") would reject a second copy anyway
	seen := make(map[string]bool)

	for _, e := range entries {
		if seen[e.md5] {
			logger.Info(nil, "scan: moving %s to errors/duplicated (duplicate within this scan)", e.path)
			MoveFailedFile(cfg.Storage.StorageDir, e.path, "duplicate", logger, nil)
			continue
		}

		if dup, ok := duplicates[e.md5]; ok {
			sha512sum, shaErr := calculateSHA512(e.path)
			if shaErr != nil {
				logger.Error(nil, "scan: sha512 %s: %v", e.path, shaErr)
			} else if sha512sum == dup.sha512 {
				logger.Info(nil, "scan: moving %s to errors/duplicated (duplicate of document %s)", e.path, dup.documentID)
				MoveFailedFile(cfg.Storage.StorageDir, e.path, "duplicate", logger, nil)
				continue
			}
			// MD5 collision with a differing SHA512 — enqueue normally
		}

		consumeTaskID := uuid.New().String()
		enrichTaskID := uuid.New().String()
		thumbnailTaskID := uuid.New().String()
		documentID := uuid.New().String()

		consumePayloadMap := map[string]any{
			"file_path":    e.path,
			"file_index":   e.index,
			"on_completed": enrichTaskID,
			"document_id":  documentID,
		}
		if cfg.Consumer.Thumbnail.Enabled {
			consumePayloadMap["on_completed_thumbnail"] = thumbnailTaskID
		}
		consumePayload, _ := json.Marshal(consumePayloadMap)
		consumePayloadPtr := json.RawMessage(consumePayload)
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   consumeTaskID,
			TaskType: "consume",
			Status:   "pending",
			BatchID:  sql.NullString{String: batchID, Valid: true},
			Payload:  &consumePayloadPtr,
			DedupKey: sql.NullString{String: "consume:" + e.md5, Valid: true},
		})
		if err != nil {
			logger.Error(nil, "scan: create consume task for %s: %v", e.path, err)
			continue
		}
		seen[e.md5] = true

		enrichPayload, _ := json.Marshal(map[string]any{
			"waiting_for": consumeTaskID,
			"file_name":   filepath.Base(e.path),
			"file_index":  e.index,
			"document_id": documentID,
		})
		enrichPayloadPtr := json.RawMessage(enrichPayload)
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   enrichTaskID,
			TaskType: "enrich",
			Status:   "waiting",
			BatchID:  sql.NullString{String: batchID, Valid: true},
			Payload:  &enrichPayloadPtr,
			DedupKey: sql.NullString{Valid: false},
		})
		if err != nil {
			logger.Error(nil, "scan: create enrich task for %s: %v", e.path, err)
			continue
		}

		if cfg.Consumer.Thumbnail.Enabled {
			thumbnailPayload, _ := json.Marshal(map[string]any{
				"waiting_for": consumeTaskID,
				"file_name":   filepath.Base(e.path),
				"file_index":  e.index,
			})
			thumbnailPayloadPtr := json.RawMessage(thumbnailPayload)
			_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
				TaskID:   thumbnailTaskID,
				TaskType: "thumbnail",
				Status:   "waiting",
				BatchID:  sql.NullString{String: batchID, Valid: true},
				Payload:  &thumbnailPayloadPtr,
				DedupKey: sql.NullString{Valid: false},
			})
			if err != nil {
				logger.Error(nil, "scan: create thumbnail task for %s: %v", e.path, err)
			}
		}
		enqueued++
	}

	// If all files were duplicates, return empty — no batch created.
	if enqueued == 0 {
		return "", 0, nil
	}

	// Create the batch only after all tasks are committed. This prevents the
	// consumer loop from picking up an incomplete or empty batch (race fix).
	err = client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Source: "polling",
		Status: "queued",
	})
	if err != nil {
		return "", 0, err
	}

	return batchID, enqueued, nil
}

// duplicateInfo pairs an existing document's ID with its stored SHA512 for
// parity verification before quarantining a scan-time duplicate
type duplicateInfo struct {
	documentID string
	sha512     string
}

// queryDuplicatesByMD5 returns a map of md5_checksum → duplicateInfo for all
// hashes that already exist in the document table.
func queryDuplicatesByMD5(ctx context.Context, client *database.Client, hashes []string) (map[string]duplicateInfo, error) {
	if len(hashes) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(hashes))
	args := make([]any, len(hashes))
	for i, h := range hashes {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = h
	}

	query := fmt.Sprintf(
		"SELECT md5_checksum, document_id, sha512_checksum FROM document WHERE md5_checksum IN (%s) AND deleted_at IS NULL",
		strings.Join(placeholders, ","),
	)

	rows, err := client.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	duplicates := make(map[string]duplicateInfo)
	for rows.Next() {
		var md5, docID, sha512 string
		if err := rows.Scan(&md5, &docID, &sha512); err != nil {
			return nil, err
		}
		duplicates[md5] = duplicateInfo{documentID: docID, sha512: sha512}
	}
	return duplicates, rows.Err()
}
