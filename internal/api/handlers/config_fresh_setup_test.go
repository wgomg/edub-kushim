package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
)

// freshSetupSettingsBody mirrors the wizard SPA's step-3 payload: full
// consumer/enricher/storage settings plus a complete database section aimed
// at the test server behind TEST_DATABASE_URL.
func freshSetupSettingsBody(t *testing.T, configDir, baseDSN string) map[string]any {
	t.Helper()
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	port := 5432
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil {
			t.Fatalf("parse port %q: %v", p, err)
		}
	}
	password, _ := u.User.Password()
	targetDB := strings.TrimPrefix(u.Path, "/")
	if targetDB == "" {
		t.Fatal("TEST_DATABASE_URL has no database name")
	}

	return map[string]any{
		"server.port":                             3000,
		"consumer.ocr.engine":                     "gosseract",
		"consumer.ocr.languages":                  []string{"eng"},
		"consumer.ocr.timeout":                    120,
		"consumer.workers":                        1,
		"consumer.pdfoptimizer.engine":            "mupdf",
		"consumer.pdfoptimizer.timeout":           0,
		"consumer.textextractor.engine":           "mupdf",
		"consumer.textextractor.timeout":          120,
		"consumer.converter.enabled":              false,
		"consumer.converter.binary":               "libreoffice",
		"consumer.converter.timeout":              300,
		"enricher.workers":                        1,
		"enricher.textreducer.engine":             "textrank",
		"enricher.textreducer.timeout":            120,
		"enricher.textreducer.target_words":       4000,
		"enricher.tagmatcher.timeout":             120,
		"enricher.tagmatcher.reduce_target_words": 4000,
		"enricher.tagmatcher.chunk_size":          4096,
		"enricher.tagmatcher.hugot.model":         "BAAI/bge-m3",
		"enricher.tagmatcher.hugot.backend":       "GO",
		"storage.storage_dir":                     filepath.Join(configDir, "storage"),
		"storage.consumption_dir":                 filepath.Join(configDir, "inbox"),
		"database.host":                           u.Hostname(),
		"database.port":                           port,
		"database.user":                           u.User.Username(),
		"database.password":                       password,
		"database.database":                       targetDB,
		"database.sslmode":                        u.Query().Get("sslmode"),
		"database.runtime":                        "host",
	}
}

func requireTestDSN(t *testing.T) string {
	t.Helper()
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return baseDSN
}

func TestPutConfig_FreshSetup_RejectsNonEmptyTargetDatabase(t *testing.T) {
	baseDSN := requireTestDSN(t)
	env := newHandlerTestEnv(t)

	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	h := disableAssetDownloads(newTestConfigHandler(cfg, nil, env.logger, nil))

	initCalls := 0
	h.OnDatabaseInit = func(context.Context) (*database.Client, *task.Enqueuer, error) {
		initCalls++
		return nil, nil, nil
	}

	body, _ := json.Marshal(freshSetupSettingsBody(t, configDir, baseDSN))
	w := rec()
	r := req(t, "PUT", "/api/v1/config", body)
	h.PutConfig(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (target database holds tables); body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not empty") {
		t.Errorf("body %q must explain the non-empty target", w.Body.String())
	}
	if initCalls != 0 {
		t.Errorf("OnDatabaseInit called %d times, want 0 after a rejected claim", initCalls)
	}
	if config.DatabaseSettingsPresent(configDir) {
		t.Error("connection keys must not be persisted when the claim fails")
	}
	if h.client != nil {
		t.Error("services must stay unbound after a rejected claim")
	}
}

func TestPutConfig_FreshSetup_InitializesClaimedTargetAndBindsServices(t *testing.T) {
	baseDSN := requireTestDSN(t)
	env := newHandlerTestEnv(t)

	configDir := t.TempDir()
	storageDir := filepath.Join(configDir, "storage")
	inboxDir := filepath.Join(configDir, "inbox")

	// the claim record of an earlier attempt whose schema step failed: the
	// connection keys are on disk, the target matches the submitted body, so
	// the retry must skip the claim and go straight to initialization
	body := freshSetupSettingsBody(t, configDir, baseDSN)
	claim := map[string]any{
		"database.host":           body["database.host"],
		"database.port":           body["database.port"],
		"database.user":           body["database.user"],
		"database.password":       body["database.password"],
		"database.database":       body["database.database"],
		"database.sslmode":        body["database.sslmode"],
		"database.runtime":        "host",
		"storage.storage_dir":     storageDir,
		"storage.consumption_dir": inboxDir,
	}
	if err := config.SaveMap(configDir, claim); err != nil {
		t.Fatalf("seed claim record: %v", err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("load seeded config: %v", err)
	}

	// present assets keep the save response free of download work
	dataDir := cfg.Consumer.OCR.DataDir
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "eng.traineddata"), []byte("stub"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "tagmatcher", "hugot", "models", "bge-m3"), 0755); err != nil {
		t.Fatal(err)
	}

	h := disableAssetDownloads(newTestConfigHandler(cfg, nil, env.logger, nil))
	initCalls := 0
	h.OnDatabaseInit = func(ctx context.Context) (*database.Client, *task.Enqueuer, error) {
		initCalls++
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("init context must carry the schema-init deadline")
		} else if remain := time.Until(deadline); remain > schemaInitTimeout || remain < 40*time.Second {
			t.Errorf("init deadline remaining = %v, want roughly %v", remain, schemaInitTimeout)
		}
		return env.client, newHandlerTestEnqueuer(env.client), nil
	}

	body["server.port"] = 3999
	encoded, _ := json.Marshal(body)
	w := rec()
	r := req(t, "PUT", "/api/v1/config", encoded)
	h.PutConfig(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["configured"] != true {
		t.Errorf("configured = %v, want true", resp["configured"])
	}
	if initCalls != 1 {
		t.Errorf("OnDatabaseInit called %d times, want exactly 1", initCalls)
	}
	if h.client == nil {
		t.Fatal("services must be bound after a successful initialization")
	}
	reloaded, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Srv.Port != 3999 {
		t.Errorf("persisted server.port = %d, want 3999 (body keys must be saved)", reloaded.Srv.Port)
	}
	if reloaded.Db.Database != body["database.database"] {
		t.Errorf("persisted database.database = %q, want %q", reloaded.Db.Database, body["database.database"])
	}
}
