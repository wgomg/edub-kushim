package handlers

import (
	"database/sql"
	"testing"
)

func snull(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func TestTaskLabel(t *testing.T) {
	tests := []struct {
		name     string
		taskType string
		dedupKey sql.NullString
		want     string
	}{
		{"backup full from dedup key", "backup", snull("backup:full:2026-08-25"), "Backup (full)"},
		{"backup database from dedup key", "backup", snull("backup:database:2026-08-25"), "Backup (database)"},
		{"config database migration", "config", snull("config:migrate-db"), "Database migration"},
		{"config storage migration", "config", snull("config:migrate-storage"), "Storage migration"},
		{"config tessdata with language", "config", snull("config:tessdata:eng"), "Download tessdata (eng)"},
		{"config hugot model", "config", snull("config:hugot"), "Download Hugot model"},
		{"enrich without dedup key", "enrich", sql.NullString{}, "Enrich"},
		{"thumbnail without dedup key", "thumbnail", snull(""), "Thumbnail"},
		{"consume without dedup key", "consume", sql.NullString{}, "Consume"},
		{"unknown type and key passthrough", "unknown", snull("other:key"), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskLabel(tt.taskType, tt.dedupKey); got != tt.want {
				t.Fatalf("taskLabel(%q, %q) = %q, want %q", tt.taskType, tt.dedupKey.String, got, tt.want)
			}
		})
	}
}
