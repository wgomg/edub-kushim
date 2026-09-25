package task

import (
	"encoding/json"
	"path/filepath"
)

// TaskPayload is the display-relevant subset of a task's JSON payload.
type TaskPayload struct {
	FilePath   string `json:"file_path"`
	FileName   string `json:"file_name"`
	FileIndex  int    `json:"file_index"` // one-based
	DocumentID string `json:"document_id"`
}

// ReadPayload decodes a task payload into the display fields, ignoring
// decode errors; a nil payload yields the zero value.
func ReadPayload(payload *json.RawMessage) TaskPayload {
	var p TaskPayload
	if payload == nil {
		return p
	}
	_ = json.Unmarshal(*payload, &p)
	return p
}

// DisplayName is the file_path base name when set, otherwise file_name.
func (p TaskPayload) DisplayName() string {
	if p.FilePath != "" {
		return filepath.Base(p.FilePath)
	}
	return p.FileName
}
