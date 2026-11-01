package textreducer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

// ReduceChunkSize is the word-window size every reduction is scored in, the value ReduceContent passes to
// the reducer. It belongs to the reduction cache fingerprint, so changing it invalidates stored reductions
// instead of mixing outputs produced under different chunk windows.
const ReduceChunkSize = 150

// reductionVersion pins the reducer's algorithm — its scoring weights, diversity penalty and text cleanup —
// the part of the output the cache key cannot derive from the input. Bump it whenever any of them changes.
const reductionVersion = "v2"

// reductionFingerprint identifies the reducer configuration that produced a stored reduction: the algorithm
// version and the chunk window. The resolved target is not part of it; it keys each cache row instead.
func reductionFingerprint(chunkSize int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "version=%s\nchunk_size=%d", reductionVersion, chunkSize))
	return hex.EncodeToString(sum[:])
}

// generationMarker identifies the configuration a batch of stored reductions was produced under, for the
// cache's clear-on-change check: the reducer version and chunk window plus the raw reduction targets the
// callers read. A marker change retires every stored row, including entries for targets no longer configured.
func generationMarker(cfg *config.Config) string {
	sum := sha256.Sum256(fmt.Appendf(nil,
		"version=%s\nchunk_size=%d\nengine=%s\nllm_target=%d\nmatcher_target=%d",
		reductionVersion, ReduceChunkSize,
		cfg.Enricher.TextReducer.Engine,
		cfg.Enricher.TextReducer.TargetWords,
		cfg.Enricher.TagMatcher.ReduceTargetWords,
	))
	return hex.EncodeToString(sum[:])
}

// ReductionCache persists reduced document texts keyed by the source text's sha256, the reducer fingerprint
// and the resolved target word count. It exists so a text reduced once — by enrichment at consume time, then
// by the audit or reconcile sweeps later — is reduced once. A nil cache is disabled: lookups report a miss
// and writes are no-ops. Read and write failures are logged and degrade to a miss, never to a failed
// reduction. All methods are safe for concurrent use.
type ReductionCache struct {
	logger      *log.Logger
	client      *database.Client
	fingerprint string
	marker      string

	mu         sync.Mutex
	generation atomic.Bool
}

// NewReductionCache binds the cache to the reducer configuration: the fingerprint and generation marker are
// derived from cfg here, so every process sharing the table keys and rotates it identically.
func NewReductionCache(logger *log.Logger, client *database.Client, cfg *config.Config) *ReductionCache {
	return &ReductionCache{
		logger:      logger,
		client:      client,
		fingerprint: reductionFingerprint(ReduceChunkSize),
		marker:      generationMarker(cfg),
	}
}

// GetExact returns the reduction stored for input at exactly target. A nil cache, an absent row, and a
// read failure all report a miss; only the failure is logged.
func (c *ReductionCache) GetExact(ctx context.Context, input string, target int) (string, bool) {
	if c == nil {
		return "", false
	}
	c.ensureGeneration(ctx)

	hash := sha256.Sum256([]byte(input))
	text, err := c.client.Queries.GetTextReduction(ctx, database.GetTextReductionParams{
		Fingerprint: c.fingerprint,
		InputHash:   hash[:],
		TargetWords: int32(target),
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.logger.Warn("", "reduction cache read: %v", err)
		}
		return "", false
	}
	return text, true
}

// Put stores the reducer's output verbatim under the input's fingerprint and target. A row already present
// for that key is kept (first writer wins), and a failed insert is logged without affecting the caller.
func (c *ReductionCache) Put(ctx context.Context, input string, target int, reduced string) {
	if c == nil {
		return
	}
	c.ensureGeneration(ctx)

	hash := sha256.Sum256([]byte(input))
	if err := c.client.Queries.InsertTextReduction(ctx, database.InsertTextReductionParams{
		Fingerprint: c.fingerprint,
		InputHash:   hash[:],
		TargetWords: int32(target),
		ReducedText: reduced,
	}); err != nil {
		c.logger.Warn("", "reduction cache write: %v", err)
	}
}

// ensureGeneration runs the once-per-process rotation check: a stored marker naming a different configuration
// retires every row, so the table is cleared and the marker rewritten. A failed check is logged and retried
// on the next call.
func (c *ReductionCache) ensureGeneration(ctx context.Context) {
	if c.generation.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation.Load() {
		return
	}
	if err := c.rotateGeneration(ctx); err != nil {
		c.logger.Warn("", "reduction cache generation check: %v", err)
		return
	}
	c.generation.Store(true)
}

// rotateGeneration clears a retired generation under a transaction-scoped advisory lock: unlike the matcher
// daemon's single startup check, several queue children can race this one, and a late truncate would
// otherwise wipe rows a sibling had just repopulated.
func (c *ReductionCache) rotateGeneration(ctx context.Context) error {
	tx, err := c.client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reduction cache rotation: %w", err)
	}
	defer tx.Rollback()
	q := c.client.Queries.WithTx(tx)

	if err := q.LockTextReductionGeneration(ctx); err != nil {
		return fmt.Errorf("lock reduction cache marker: %w", err)
	}
	stored, err := q.GetTextReductionGeneration(ctx)
	switch {
	case err == nil && stored == c.marker:
		return tx.Commit()
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read reduction cache marker: %w", err)
	}
	if err := q.TruncateTextReduction(ctx); err != nil {
		return fmt.Errorf("clear reduction cache: %w", err)
	}
	if err := q.SetTextReductionGeneration(ctx, c.marker); err != nil {
		return fmt.Errorf("write reduction cache marker: %w", err)
	}
	return tx.Commit()
}
