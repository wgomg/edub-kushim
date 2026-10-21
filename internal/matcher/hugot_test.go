//go:build cgo

package matcher

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

func TestVerifySHA256_Match(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	// sha256("hello world")
	const want = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if err := verifySHA256(path, want); err != nil {
		t.Errorf("verifySHA256: unexpected error for matching content: %v", err)
	}
}

func TestVerifySHA256_Mismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("tampered content"), 0644); err != nil {
		t.Fatal(err)
	}
	const want = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if err := verifySHA256(path, want); err == nil {
		t.Error("verifySHA256: expected error for mismatched content, got nil")
	}
}

func TestEncodeChunked_CacheHitSkipsInference(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	cache := NewTextEmbeddingCache(log.NewDiscardLogger(), q, "fp-encode-chunked")

	const input = "long reduced tag text that must come from the cache"
	want := []float32{0.1, -0.2, 0.3}
	cache.Put(ctx, input, want)

	// the pipeline is nil on purpose: a miss reaches the tokenizer and panics, a hit must return
	// the cached vector without touching inference
	h := &Hugot{logger: log.NewDiscardLogger(), textCache: cache}
	got, err := h.encodeChunked(ctx, "doc-1", input)
	if err != nil {
		t.Fatalf("encodeChunked: unexpected error on cache hit: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cached vector: got %v, want %v", got, want)
	}
}
