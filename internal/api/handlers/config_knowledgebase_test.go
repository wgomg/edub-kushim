package handlers

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
)

func knowledgeBaseTestConfig(dir string, sources []string) *config.Config {
	cfg := config.DefaultConfig(dir)
	cfg.Consumer.OCR.Languages = []string{"eng"}
	cfg.KnowledgeBase.Sources = sources
	return cfg
}

// satisfyPinnedOEWN records an oewn install at the pinned source and converter
// versions, so download-contract tests see a satisfied knowledge base without the
// network. The version literals mirror the compiled-in pin; if the pin moves, the
// pending-count assertions trip and point at the stale fixture.
func satisfyPinnedOEWN(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
INSERT INTO kb_source (source, source_version, license, notice, upstream_url,
upstream_sha256, converter_version, concept_count, lemma_count, edge_count)
VALUES ('oewn', '2025', 'fixture', 'fixture', 'fixture', 'fixture', '1', 0, 0, 0)
ON CONFLICT (source) DO NOTHING`)
	if err != nil {
		t.Fatalf("insert pinned oewn row: %v", err)
	}
}

func TestMissingAssetsKnowledgeBaseOffline(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	for _, tc := range []struct {
		name      string
		sources   []string
		wantKBKey string
	}{
		{
			name:      "unknown source is pending without a download",
			sources:   []string{"bogus-source"},
			wantKBKey: "kb:bogus-source",
		},
		{
			name:    "no configured sources add no kb key",
			sources: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newHandlerTestEnv(t)
			cfg := knowledgeBaseTestConfig(t.TempDir(), tc.sources)
			h := disableAssetDownloads(NewConfigHandler(
				func() *config.Config { return cfg },
				func(*config.Config) {},
				env.client,
				env.logger,
				env.enqueuer,
				env.services,
			))

			got := h.missingAssets(context.Background(), cfg)
			found := tc.wantKBKey != "" && slices.Contains(got, tc.wantKBKey)
			if tc.wantKBKey != "" && !found {
				t.Fatalf("missingAssets = %v, want %v", got, tc.wantKBKey)
			}
			for _, key := range got {
				if key != "eng" && key != hugotAssetKey && key != tc.wantKBKey {
					t.Fatalf("unexpected asset key %q in %v", key, got)
				}
			}
		})
	}
}

func TestFailedAssetTasksKnowledgeBase(t *testing.T) {
	h := &ConfigHandler{downloadErrs: map[string]string{"kb:oewn": "boom"}}
	got := h.failedAssetTasks([]string{"kb:oewn"})
	if len(got) != 1 {
		t.Fatalf("failedAssetTasks = %v, want 1 entry", got)
	}
	if got[0].Op != "kb" || got[0].Lang != "oewn" {
		t.Fatalf("failedAssetTasks = %+v, want Op=kb Lang=oewn", got[0])
	}
	if missing := h.failedAssetTasks([]string{"eng"}); len(missing) != 0 {
		t.Fatalf("stale failure reported: %+v", missing)
	}
}

func TestDownloadKnowledgeBaseNoClient(t *testing.T) {
	h := &ConfigHandler{}
	cfg := knowledgeBaseTestConfig(t.TempDir(), []string{"oewn"})
	if err := h.downloadKnowledgeBase(context.Background(), cfg, "oewn"); err == nil {
		t.Fatal("expected database-not-ready error")
	}
}
