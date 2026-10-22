package consumption

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ScanAndEnqueue scans the consumption directory and enqueues one consume task (with its own waiting
// enrich and, when thumbnails are enabled, a thumbnail child) per new file through the transactional
// enqueuer, deduplicating by MD5 plus SHA-512 parity against existing documents and within the scan
// itself. The batch and its tasks commit together, and a dedup-skipped consume task takes its linked
// children with it. Any paused batch skips the scan entirely, returning empty with a nil error.
func ScanAndEnqueue(ctx context.Context, cfg *config.Config, client *database.Client, enqueuer *task.Enqueuer, logger *log.Logger) (batchID string, enqueued int, err error) {
	pausedCount, err := client.Queries.CountPausedBatches(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("count paused batches: %w", err)
	}
	if pausedCount > 0 {
		logger.Info("", "scan: skipped — %d paused batch(es) exist; resolve credit issue first", pausedCount)
		return "", 0, nil
	}

	paths, err := storage.ListFilePaths(
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

	// compute MD5 for all files, skipping unreadable ones
	type fileEntry struct {
		path  string
		md5   string
		index int
	}
	var entries []fileEntry
	for i, path := range paths {
		md5hash, md5Err := storage.CalculateMD5(path)
		if md5Err != nil {
			logger.Error("", "scan: md5 %s: %v", path, md5Err)
			continue
		}
		entries = append(entries, fileEntry{path: path, md5: md5hash, index: i + 1})
	}

	if len(entries) == 0 {
		return "", 0, nil
	}

	// batch dedup: single query for all MD5 hashes instead of one per file
	md5hashes := make([]string, len(entries))
	for i, e := range entries {
		md5hashes[i] = e.md5
	}
	duplicates, dupErr := queryDuplicatesByMD5(ctx, client, md5hashes)
	if dupErr != nil {
		logger.Error("", "scan: batch dedup query: %v", dupErr)
		duplicates = make(map[string]duplicateInfo)
	}

	// hashes already enqueued earlier in this scan; the consume task's dedup key ("consume:<md5>") would
	// reject a second copy anyway
	seen := make(map[string]bool)

	var tasks []task.TaskSpec
	consumeTaskIDs := make(map[string]struct{}, len(entries))

	for _, e := range entries {
		if seen[e.md5] {
			logger.Info("", "scan: moving %s to errors/duplicated (duplicate within this scan)", e.path)
			MoveFailedFile(cfg.Storage.StorageDir, e.path, "duplicate", logger, nil)
			continue
		}

		if dup, ok := duplicates[e.md5]; ok {
			sha512sum, shaErr := calculateSHA512(e.path)
			if shaErr != nil {
				logger.Error("", "scan: sha512 %s: %v", e.path, shaErr)
			} else if sha512sum == dup.sha512 {
				logger.Info("", "scan: moving %s to errors/duplicated (duplicate of document %s)", e.path, dup.documentID)
				MoveFailedFile(cfg.Storage.StorageDir, e.path, "duplicate", logger, nil)
				continue
			}
			// collision on MD5 but a differing SHA512 — enqueue normally
		}

		fileTasks := IngestTaskSpecs(IngestFile{
			Path:       e.path,
			Index:      e.index,
			MD5:        e.md5,
			DocumentID: uuid.New().String(),
		}, cfg.Consumer.Thumbnail.Enabled)
		consumeTaskIDs[fileTasks[0].TaskID] = struct{}{}
		tasks = append(tasks, fileTasks...)
		seen[e.md5] = true
	}

	if len(tasks) == 0 {
		return "", 0, nil
	}

	spec, err := task.NewBatchSpec(types.Batch.Origin.Polling, "")
	if err != nil {
		return "", 0, fmt.Errorf("build polling batch spec: %w", err)
	}

	result, err := enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return "", 0, err
	}
	for _, taskID := range result.TaskIDs {
		if _, ok := consumeTaskIDs[taskID]; ok {
			enqueued++
		}
	}

	return result.BatchID, enqueued, nil
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
