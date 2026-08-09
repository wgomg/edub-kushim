package configtask

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestDirChanged(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want bool
	}{
		{"both empty", "", "", false},
		{"old empty", "", "/x", false},
		{"new empty", "/x", "", false},
		{"same path", "/a", "/a", false},
		{"trailing slash vs not", "/a/", "/a", false},
		{"canonical equivalent", "/foo/../bar", "/bar", false},
		{"different paths", "/a", "/b", true},
		{"different subdirs", "/a/x", "/a/y", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DirChanged(tt.old, tt.new); got != tt.want {
				t.Errorf("DirChanged(%q, %q) = %v, want %v", tt.old, tt.new, got, tt.want)
			}
		})
	}
}

func TestMigrateStorage_NoOpReturnsErrNoOp(t *testing.T) {
	logger := testutil.NewTestLogger()
	err := MigrateStorage(context.Background(), logger, MigrateStoragePayload{
		ConfigDir:         t.TempDir(),
		OldStorageDir:     "/a",
		NewStorageDir:     "/a",
		OldConsumptionDir: "/b",
		NewConsumptionDir: "/b",
	})
	if !errors.Is(err, ErrNoOp) {
		t.Errorf("MigrateStorage no-op err = %v, want ErrNoOp", err)
	}
}

func TestHandleMigrateStorage_NoOpReturnsNoOpStatus(t *testing.T) {
	h := NewConfigTaskHandler(testutil.NewTestLogger())
	payload, _ := json.Marshal(MigrateStoragePayload{
		Op:                opMigrateStorage,
		ConfigDir:         t.TempDir(),
		OldStorageDir:     "/a",
		NewStorageDir:     "/a",
		OldConsumptionDir: "/b",
		NewConsumptionDir: "/b",
	})
	result, err := h.Handle(context.Background(), task.Task{TaskID: "noop", Payload: payload})
	if err != nil {
		t.Fatalf("Handle no-op: %v", err)
	}
	var status map[string]string
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if status["status"] != "no-op" {
		t.Errorf("status = %q, want %q", status["status"], "no-op")
	}
}
