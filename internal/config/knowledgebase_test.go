package config

import "testing"

func TestDefaultConfig_KnowledgeBaseDefaultsOewn(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if len(cfg.KnowledgeBase.Sources) != 1 || cfg.KnowledgeBase.Sources[0] != "oewn" {
		t.Errorf("KnowledgeBase.Sources = %v, want [oewn]", cfg.KnowledgeBase.Sources)
	}
}

func TestFinalizeConfig_KnowledgeBaseSources(t *testing.T) {
	configDir := t.TempDir()

	t.Run("accepts pinned sources", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.KnowledgeBase.Sources = []string{"oewn", "conceptnet"}
		cfg.Consumer.OCR.Languages = []string{"eng"}

		if err := finalizeConfig(cfg, configDir); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
	})

	t.Run("rejects unknown source", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.KnowledgeBase.Sources = []string{"oewn", "wordnet-x"}
		cfg.Consumer.OCR.Languages = []string{"eng"}

		if err := finalizeConfig(cfg, configDir); err == nil {
			t.Fatal("expected error for unknown knowledge base source")
		}
	})

	t.Run("empty list opts out", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.KnowledgeBase.Sources = nil
		cfg.Consumer.OCR.Languages = []string{"eng"}

		if err := finalizeConfig(cfg, configDir); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
	})
}
