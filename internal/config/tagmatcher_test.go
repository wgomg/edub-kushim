package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_MinSuggestionSimilarity pins the suggestion-injection bar
// contract: an absent or zero key reverts to the per-model default, an
// explicit key is honored, and a negative bar is rejected.
func TestLoad_MinSuggestionSimilarity(t *testing.T) {
	loadBar := func(t *testing.T, yaml string) (*Config, error) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		return Load(dir)
	}
	base := "consumer:\n  ocr:\n    languages:\n      - eng\n"

	t.Run("unset derives the bge-m3 default", func(t *testing.T) {
		cfg, err := loadBar(t, base)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Enricher.TagMatcher.MinSuggestionSimilarity; got != 0.55 {
			t.Errorf("MinSuggestionSimilarity = %v, want 0.55", got)
		}
	})

	t.Run("zero reverts to the model default", func(t *testing.T) {
		cfg, err := loadBar(t, base+"enricher:\n  tagmatcher:\n    min_suggestion_similarity: 0\n")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Enricher.TagMatcher.MinSuggestionSimilarity; got != 0.55 {
			t.Errorf("MinSuggestionSimilarity = %v, want the 0.55 default after an explicit zero", got)
		}
	})

	t.Run("explicit key is honored", func(t *testing.T) {
		cfg, err := loadBar(t, base+"enricher:\n  tagmatcher:\n    min_suggestion_similarity: 0.7\n")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Enricher.TagMatcher.MinSuggestionSimilarity; got != 0.7 {
			t.Errorf("MinSuggestionSimilarity = %v, want 0.7", got)
		}
	})

	t.Run("another model derives its own default", func(t *testing.T) {
		cfg, err := loadBar(t, base+"enricher:\n  tagmatcher:\n    hugot:\n      model: sentence-transformers/all-MiniLM-L6-v2\n")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Enricher.TagMatcher.MinSuggestionSimilarity; got != 0.40 {
			t.Errorf("MinSuggestionSimilarity = %v, want 0.40 for all-MiniLM-L6-v2", got)
		}
	})

	t.Run("negative is rejected", func(t *testing.T) {
		_, err := loadBar(t, base+"enricher:\n  tagmatcher:\n    min_suggestion_similarity: -0.1\n")
		if err == nil {
			t.Fatal("a negative bar must fail the load")
		}
		if !strings.Contains(err.Error(), "min_suggestion_similarity") {
			t.Errorf("error %q does not name the offending key", err)
		}
	})
}
