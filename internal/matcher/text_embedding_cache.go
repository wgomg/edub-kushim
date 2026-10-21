package matcher

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

// embeddingCacheGeneration must be bumped whenever the embedding runtime stack changes in a way that the
// fingerprint's other inputs cannot see — a hugot or tokenizer upgrade, a pipeline option change — so
// that vectors from the previous stack stop matching without an operator action.
const embeddingCacheGeneration = "v1"

// TextEmbeddingCache persists one pooled embedding per distinct chunked input text, keyed by the text's
// sha256 and the embedding stack's fingerprint. It exists so the same text — the document enrichment
// already embeds at consume time, then the audit embeds again weeks later — is embedded once. A nil
// cache is disabled: Get reports a miss and Put is a no-op. All methods are safe for concurrent use; a
// read or write failure is logged and degrades to a miss, never to a failed match.
type TextEmbeddingCache struct {
	logger      *log.Logger
	queries     *database.Queries
	fingerprint string
}

// NewTextEmbeddingCache binds the cache to the embedding stack's fingerprint, the value that NewHugot
// derives; every lookup and insert is keyed by it plus the input text's hash.
func NewTextEmbeddingCache(logger *log.Logger, queries *database.Queries, fingerprint string) *TextEmbeddingCache {
	return &TextEmbeddingCache{logger: logger, queries: queries, fingerprint: fingerprint}
}

// Get returns the cached vector for input. A miss, a malformed row, or any read failure reports ok=false
// so the caller computes the embedding instead.
func (c *TextEmbeddingCache) Get(ctx context.Context, input string) ([]float32, bool) {
	if c == nil {
		return nil, false
	}

	sum := sha256.Sum256([]byte(input))
	raw, err := c.queries.GetMatcherTextEmbedding(ctx, database.GetMatcherTextEmbeddingParams{
		Fingerprint: c.fingerprint,
		InputHash:   sum[:],
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.logger.Warn("", "matcher text embedding cache read: %v", err)
		}
		return nil, false
	}

	vector, err := decodeEmbedding(raw)
	if err != nil {
		c.logger.Warn("", "matcher text embedding cache row: %v", err)
		return nil, false
	}
	return vector, true
}

// Put stores the pooled vector for input. An empty vector is never cached; a write failure is logged and
// does not affect the caller.
func (c *TextEmbeddingCache) Put(ctx context.Context, input string, embedding []float32) {
	if c == nil || len(embedding) == 0 {
		return
	}

	sum := sha256.Sum256([]byte(input))
	err := c.queries.InsertMatcherTextEmbedding(ctx, database.InsertMatcherTextEmbeddingParams{
		Fingerprint: c.fingerprint,
		InputHash:   sum[:],
		Embedding:   encodeEmbedding(embedding),
	})
	if err != nil {
		c.logger.Warn("", "matcher text embedding cache write: %v", err)
	}
}

// ClearStaleGeneration clears the cache when it holds rows from a different embedding stack, using a
// stored generation marker so the steady-state start does one indexed read instead of a table scan. It
// reports whether it cleared the cache, for the startup log line, and runs once when the daemon starts.
// A clear leaves an empty cache that the normal write path repopulates.
func (c *TextEmbeddingCache) ClearStaleGeneration(ctx context.Context) (bool, error) {
	if c == nil {
		return false, nil
	}

	stored, err := c.queries.GetMatcherTextEmbeddingGeneration(ctx)
	switch {
	case err == nil && stored == c.fingerprint:
		return false, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("read matcher text embedding generation: %w", err)
	}

	if err := c.queries.TruncateMatcherTextEmbedding(ctx); err != nil {
		return false, fmt.Errorf("clear matcher text embeddings: %w", err)
	}
	if err := c.queries.SetMatcherTextEmbeddingGeneration(ctx, c.fingerprint); err != nil {
		return false, fmt.Errorf("write matcher text embedding generation: %w", err)
	}
	return true, nil
}

// embeddingFingerprint identifies the embedding stack that produced a vector: the model file's contents,
// the backend name, the effective chunk size and overlap, an optional runtime pin, and the cache generation
// constant. runtimePin carries the pinned backend runtime identity (the ORT archive hash) and is empty for
// the Go backend. The model file is hashed in a stream; both backends load model.onnx from the model path.
func embeddingFingerprint(modelPath, backend string, chunkSize, overlap int, runtimePin string) (string, error) {
	modelFile := filepath.Join(modelPath, "model.onnx")
	file, err := os.Open(modelFile)
	if err != nil {
		return "", fmt.Errorf("open %s for embedding fingerprint: %w", modelFile, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s for embedding fingerprint: %w", modelFile, err)
	}
	fmt.Fprintf(hash, "\nbackend=%s\nchunk_size=%d\nchunk_overlap=%d\nruntime_pin=%s\ngeneration=%s",
		backend, chunkSize, overlap, runtimePin, embeddingCacheGeneration)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// decodeEmbedding turns little-endian float32 bytes into a vector; an empty slice or a length that is not
// a multiple of four is rejected as malformed.
func decodeEmbedding(raw []byte) ([]float32, error) {
	if len(raw) == 0 || len(raw)%4 != 0 {
		return nil, fmt.Errorf("malformed embedding of %d bytes", len(raw))
	}

	vector := make([]float32, len(raw)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return vector, nil
}

// encodeEmbedding is decodeEmbedding's exact inverse: it writes little-endian float32 so the stored layout
// is independent of the host byte order, and the two helpers must change together.
func encodeEmbedding(vector []float32) []byte {
	raw := make([]byte, len(vector)*4)
	for i, v := range vector {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	return raw
}
