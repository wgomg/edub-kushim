package configtask

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/wgomg/edub-kushim/internal/testutil"
)

// An already-expired deadline must abort the migration at its first
// context-aware step: without the handler's timeout wrap the full dump would
// run and return nil.
func TestMigrateDBTask_DeadlineAbortsBeforeDump(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	runtime, container := containerRuntime(t)
	queries, _ := database.NewTestQueries(t)

	configDir := t.TempDir()
	storageDir := filepath.Join(configDir, "storage")
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		t.Fatal(err)
	}

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
	if err := config.SaveMap(configDir, map[string]any{
		"database.host":           u.Hostname(),
		"database.port":           port,
		"database.user":           u.User.Username(),
		"database.password":       password,
		"database.database":       strings.TrimPrefix(u.Path, "/"),
		"database.sslmode":        u.Query().Get("sslmode"),
		"database.runtime":        runtime,
		"database.container":      container,
		"storage.storage_dir":     storageDir,
		"storage.consumption_dir": filepath.Join(configDir, "inbox"),
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}

	previous := migrateDBTimeout
	migrateDBTimeout = time.Nanosecond
	t.Cleanup(func() { migrateDBTimeout = previous })

	payload, _ := json.Marshal(MigrateDBPayload{ConfigDir: configDir})
	h := NewMigrateDBTaskHandler(queries, testutil.NewTestLogger())
	_, err = h.Handle(context.Background(), task.Task{TaskID: "deadline-test", Payload: payload})
	if err == nil {
		t.Fatal("Handle must fail when its deadline is already expired")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Handle error = %v, want context.DeadlineExceeded", err)
	}
}
