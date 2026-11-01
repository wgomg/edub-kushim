//go:build cgo

package matcher

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gomlx/go-huggingface/tokenizers/hftokenizer"
	"github.com/knights-analytics/hugot/backends"
	"github.com/knights-analytics/hugot/pipelines"

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

// minimalWordPieceJSON is a tiny WordPiece tokenizer.json covering the test vocabulary, so Encode can
// classify by token count without any model files.
var minimalWordPieceJSON = []byte(`{
	"version": "1.0",
	"truncation": null,
	"padding": null,
	"added_tokens": [],
	"normalizer": {"type": "BertNormalizer", "lowercase": true},
	"pre_tokenizer": {"type": "BertPreTokenizer"},
	"post_processor": null,
	"decoder": {"type": "WordPiece", "prefix": "##"},
	"model": {
		"type": "WordPiece",
		"unk_token": "[UNK]",
		"continuing_subword_prefix": "##",
		"max_input_chars_per_word": 100,
		"vocab": {"[UNK]": 0, "hello": 1, "world": 2}
	}
}`)

func newTestTokenizer(t *testing.T) *backends.Tokenizer {
	t.Helper()
	tk, err := hftokenizer.NewFromContent(nil, minimalWordPieceJSON)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	return &backends.Tokenizer{GoTokenizer: &backends.GoTokenizer{Tokenizer: tk}}
}

func TestEncode_ShortCacheHitSkipsInference(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	cache := NewTextEmbeddingCache(log.NewDiscardLogger(), q, "fp-encode-short")

	const input = "hello world"
	want := []float32{0.1, -0.2, 0.3}
	cache.Put(ctx, input, want)

	// pipeline holds only a tokenizer: a miss would panic in RunPipeline, a hit must return the vector first
	h := &Hugot{
		logger:    log.NewDiscardLogger(),
		textCache: cache,
		pipeline: &pipelines.FeatureExtractionPipeline{
			BasePipeline: &backends.BasePipeline{Model: &backends.Model{Tokenizer: newTestTokenizer(t)}},
		},
		chunkSize: 512,
	}
	got, err := h.Encode(ctx, "doc-1", []string{input})
	if err != nil {
		t.Fatalf("Encode: unexpected error on short cache hit: %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("cached vector: got %v, want %v", got, want)
	}
}
