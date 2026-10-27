package handlers

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/types"
)

func TestTaskLabel(t *testing.T) {
	tests := []struct {
		name     string
		taskType string
		payload  string
		want     string
	}{
		{"backup full from payload mode", "backup", `{"mode":"full"}`, "Backup (full)"},
		{"backup database from payload mode", "backup", `{"mode":"database"}`, "Backup (database)"},
		{"backup without a mode defaults to full", "backup", `{}`, "Backup (full)"},
		{"backup with an empty payload defaults to full", "backup", ``, "Backup (full)"},
		{"mirror from task type", "mirror", `{}`, "Mirror"},
		{"database migration from task type", "migrate-db", `{}`, "Database migration"},
		{"storage migration from task type", "migrate-storage", `{}`, "Storage migration"},
		{"enrich from task type", "enrich", `{}`, "Enrich"},
		{"thumbnail from task type", "thumbnail", `{}`, "Thumbnail"},
		{"consume from task type", "consume", `{}`, "Consume"},
		{"unknown type passthrough", "unknown", `{}`, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskLabel(types.TaskType(tt.taskType), json.RawMessage(tt.payload)); got != tt.want {
				t.Fatalf("taskLabel(%q, %s) = %q, want %q", tt.taskType, tt.payload, got, tt.want)
			}
		})
	}
}
