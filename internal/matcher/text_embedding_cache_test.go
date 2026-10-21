package matcher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

func TestEmbeddingCodecRoundTrip(t *testing.T) {
	want := []float32{1, -0.5, 3.25, 1e-30, 0}
	raw := encodeEmbedding(want)

	got, err := decodeEmbedding(raw)
	if err != nil {
		t.Fatalf("decodeEmbedding: unexpected error: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("round trip: got %v, want %v", got, want)
	}
	if !bytes.Equal(raw[:4], []byte{0x00, 0x00, 0x80, 0x3F}) {
		t.Fatalf("1.0 encodes as %v, want little-endian 00 00 80 3F: a layout change without "+
			"bumping embeddingCacheGeneration misreads rows stored by the previous build", raw[:4])
	}
}

func TestDecodeEmbeddingRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"single byte", []byte{1}},
		{"truncated float", []byte{1, 2, 3}},
		{"partial trailing float", []byte{1, 2, 3, 4, 5}},
	}
	for _, tc := range cases {
		if _, err := decodeEmbedding(tc.raw); err == nil {
			t.Errorf("%s: decodeEmbedding accepted a malformed row", tc.name)
		}
	}
}

func TestEmbeddingFingerprint(t *testing.T) {
	modelDir := t.TempDir()
	modelFile := filepath.Join(modelDir, "model.onnx")
	writeModel := func(content string) {
		t.Helper()
		if err := os.WriteFile(modelFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeModel("model content A")
	base, err := embeddingFingerprint(modelDir, "go", 4096, 410, "")
	if err != nil {
		t.Fatalf("embeddingFingerprint: unexpected error: %v", err)
	}
	again, err := embeddingFingerprint(modelDir, "go", 4096, 410, "")
	if err != nil || again != base {
		t.Fatalf("same stack fingerprinted as (%q, %v), want %q", again, err, base)
	}

	writeModel("model content B")
	rewritten, err := embeddingFingerprint(modelDir, "go", 4096, 410, "")
	if err != nil {
		t.Fatalf("embeddingFingerprint after model rewrite: %v", err)
	}
	if rewritten == base {
		t.Fatal("model content change did not move the fingerprint — cached vectors from the old model would be reused")
	}

	variants := []struct {
		name       string
		backend    string
		chunkSize  int
		overlap    int
		runtimePin string
	}{
		{"different backend", "ort", 4096, 410, ""},
		{"different chunk size", "go", 2048, 410, ""},
		{"different overlap", "go", 4096, 205, ""},
		{"different runtime pin", "go", 4096, 410, "sha256:abc"},
	}
	for _, v := range variants {
		fp, err := embeddingFingerprint(modelDir, v.backend, v.chunkSize, v.overlap, v.runtimePin)
		if err != nil {
			t.Fatalf("%s: embeddingFingerprint: %v", v.name, err)
		}
		if fp == rewritten {
			t.Errorf("%s: fingerprint matched the base stack — cached vectors from a different stack would be reused", v.name)
		}
	}

	if _, err := embeddingFingerprint(filepath.Join(t.TempDir(), "absent"), "go", 4096, 410, ""); err == nil {
		t.Error("missing model file: expected an error, got nil")
	}
}

func TestTextEmbeddingCacheDisabledOnNil(t *testing.T) {
	var cache *TextEmbeddingCache
	ctx := context.Background()

	if _, ok := cache.Get(ctx, "any input"); ok {
		t.Error("nil cache reported a hit, want miss")
	}
	cache.Put(ctx, "any input", []float32{1})

	cleared, err := cache.ClearStaleGeneration(ctx)
	if err != nil || cleared {
		t.Errorf("nil cache ClearStaleGeneration = (%t, %v), want (false, nil)", cleared, err)
	}
}

func TestTextEmbeddingCacheRoundTrip(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	cache := NewTextEmbeddingCache(log.NewDiscardLogger(), q, "fp-roundtrip")

	if err := q.TruncateMatcherTextEmbedding(ctx); err != nil {
		t.Fatalf("clear cache table: %v", err)
	}
	const input = "reduced tag text used by the cache round trip test"
	if _, ok := cache.Get(ctx, input); ok {
		t.Fatal("Get hit an empty table, want miss")
	}

	want := []float32{0.25, -0.75, 1.5, -1e-30}
	cache.Put(ctx, input, want)
	got, ok := cache.Get(ctx, input)
	if !ok {
		t.Fatal("Get missed the row Put just wrote")
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stored vector: got %v, want %v", got, want)
	}
	if _, ok := cache.Get(ctx, input+" (never stored)"); ok {
		t.Fatal("Get hit a text that was never stored")
	}

	cache.Put(ctx, "empty vector is never cached", nil)
	if _, ok := cache.Get(ctx, "empty vector is never cached"); ok {
		t.Fatal("Put stored an empty vector, want it skipped")
	}
}

func TestTextEmbeddingCacheClearStaleGeneration(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	defer db.Close()
	ctx := context.Background()
	logger := log.NewDiscardLogger()
	vec := []float32{0.5, -0.5}

	t.Run("matching generation keeps rows", func(t *testing.T) {
		if err := q.SetMatcherTextEmbeddingGeneration(ctx, "fp-match"); err != nil {
			t.Fatalf("set generation marker: %v", err)
		}
		cache := NewTextEmbeddingCache(logger, q, "fp-match")
		cache.Put(ctx, "kept text", vec)

		cleared, err := cache.ClearStaleGeneration(ctx)
		if err != nil {
			t.Fatalf("ClearStaleGeneration: %v", err)
		}
		if cleared {
			t.Error("cache cleared although the stored generation matches")
		}
		if _, ok := cache.Get(ctx, "kept text"); !ok {
			t.Error("row lost although the stored generation matches")
		}
	})

	t.Run("different generation truncates and records the new one", func(t *testing.T) {
		if err := q.SetMatcherTextEmbeddingGeneration(ctx, "fp-old"); err != nil {
			t.Fatalf("set generation marker: %v", err)
		}
		cache := NewTextEmbeddingCache(logger, q, "fp-new")
		cache.Put(ctx, "dropped text", vec)

		cleared, err := cache.ClearStaleGeneration(ctx)
		if err != nil {
			t.Fatalf("ClearStaleGeneration: %v", err)
		}
		if !cleared {
			t.Fatal("stale generation not cleared")
		}
		if _, ok := cache.Get(ctx, "dropped text"); ok {
			t.Error("rows from the previous generation survived the clear")
		}
		stored, err := q.GetMatcherTextEmbeddingGeneration(ctx)
		if err != nil {
			t.Fatalf("read generation marker: %v", err)
		}
		if stored != "fp-new" {
			t.Errorf("generation marker = %q, want fp-new", stored)
		}
	})

	t.Run("missing marker truncates", func(t *testing.T) {
		if _, err := db.ExecContext(ctx,
			`DELETE FROM system_signal WHERE name = 'matcher_text_embedding_generation'`); err != nil {
			t.Fatalf("drop generation marker: %v", err)
		}
		cache := NewTextEmbeddingCache(logger, q, "fp-fresh")
		cache.Put(ctx, "orphan text", vec)

		cleared, err := cache.ClearStaleGeneration(ctx)
		if err != nil {
			t.Fatalf("ClearStaleGeneration: %v", err)
		}
		if !cleared {
			t.Fatal("cache kept rows although no generation marker exists")
		}
		if _, ok := cache.Get(ctx, "orphan text"); ok {
			t.Error("rows written without a generation marker survived the clear")
		}
	})
}
