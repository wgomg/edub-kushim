package textreducer

import (
	"context"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

// cacheConfig builds the configuration fields the generation marker hashes at one target.
func cacheConfig(targetWords int) *config.Config {
	return &config.Config{Enricher: config.EnricherConfig{
		TextReducer: config.TextReducerConfig{TargetWords: targetWords},
		TagMatcher:  config.TagMatcherConfig{ReduceTargetWords: targetWords},
	}}
}

// resetReductionCacheTable clears the cache rows and the generation marker so each test's first
// cache call rotates from a known state.
func resetReductionCacheTable(t *testing.T, client *database.Client) {
	t.Helper()
	ctx := context.Background()
	if err := client.Queries.TruncateTextReduction(ctx); err != nil {
		t.Fatalf("truncate text_reduction: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx,
		`DELETE FROM system_signal WHERE name = 'text_reduction_generation'`); err != nil {
		t.Fatalf("drop generation marker: %v", err)
	}
}

func TestReductionCacheDisabledOnNil(t *testing.T) {
	var cache *ReductionCache
	ctx := context.Background()

	if _, ok := cache.GetExact(ctx, "any input", 4000); ok {
		t.Error("nil cache reported a hit, want miss")
	}
	cache.Put(ctx, "any input", 4000, "reduced")
}

func TestReductionCacheRoundTrip(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	resetReductionCacheTable(t, client)
	ctx := context.Background()
	cache := NewReductionCache(log.NewDiscardLogger(), client, cacheConfig(4000))

	const input = "long document text reduced for the cache round trip test"
	if _, ok := cache.GetExact(ctx, input, 4000); ok {
		t.Fatal("GetExact hit an empty table, want miss")
	}

	const want = "compressed reduction stored by the round trip test"
	cache.Put(ctx, input, 4000, want)
	got, ok := cache.GetExact(ctx, input, 4000)
	if !ok {
		t.Fatal("GetExact missed the row Put just wrote")
	}
	if got != want {
		t.Fatalf("stored text: got %q, want %q", got, want)
	}

	if _, ok := cache.GetExact(ctx, input, 3000); ok {
		t.Fatal("GetExact hit a different target, the resolved target keys each row")
	}
	if _, ok := cache.GetExact(ctx, input+" (never stored)", 4000); ok {
		t.Fatal("GetExact hit a text that was never stored")
	}

	cache.Put(ctx, input, 4000, "second writer")
	if got, _ := cache.GetExact(ctx, input, 4000); got != want {
		t.Fatalf("stored text after a second Put: got %q, want the first writer %q", got, want)
	}
}

func TestReductionCacheGenerationRotation(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	ctx := context.Background()
	const input = "text stored before the configuration check"

	t.Run("matching config keeps rows", func(t *testing.T) {
		resetReductionCacheTable(t, client)
		cfg := cacheConfig(4000)
		NewReductionCache(log.NewDiscardLogger(), client, cfg).Put(ctx, input, 4000, "kept reduction")

		fresh := NewReductionCache(log.NewDiscardLogger(), client, cfg)
		got, ok := fresh.GetExact(ctx, input, 4000)
		if !ok || got != "kept reduction" {
			t.Fatalf("row lost on a matching config: got (%q, %t), want the stored reduction", got, ok)
		}
	})

	t.Run("changed config truncates and records the marker", func(t *testing.T) {
		resetReductionCacheTable(t, client)
		NewReductionCache(log.NewDiscardLogger(), client, cacheConfig(4000)).Put(ctx, input, 4000, "retired reduction")

		reconfigured := NewReductionCache(log.NewDiscardLogger(), client, cacheConfig(3000))
		if _, ok := reconfigured.GetExact(ctx, input, 4000); ok {
			t.Fatal("rows from the previous configuration survived the rotation")
		}
		stored, err := client.Queries.GetTextReductionGeneration(ctx)
		if err != nil {
			t.Fatalf("read generation marker: %v", err)
		}
		if want := generationMarker(cacheConfig(3000)); stored != want {
			t.Errorf("generation marker = %q, want %q", stored, want)
		}
	})

	t.Run("missing marker truncates", func(t *testing.T) {
		resetReductionCacheTable(t, client)
		cfg := cacheConfig(4000)
		NewReductionCache(log.NewDiscardLogger(), client, cfg).Put(ctx, input, 4000, "orphaned reduction")
		if _, err := client.DB().ExecContext(ctx,
			`DELETE FROM system_signal WHERE name = 'text_reduction_generation'`); err != nil {
			t.Fatalf("drop generation marker: %v", err)
		}

		fresh := NewReductionCache(log.NewDiscardLogger(), client, cfg)
		if _, ok := fresh.GetExact(ctx, input, 4000); ok {
			t.Fatal("rows written without a generation marker survived the rotation")
		}
		stored, err := client.Queries.GetTextReductionGeneration(ctx)
		if err != nil {
			t.Fatalf("read generation marker: %v", err)
		}
		if want := generationMarker(cfg); stored != want {
			t.Errorf("generation marker = %q, want %q", stored, want)
		}
	})
}
