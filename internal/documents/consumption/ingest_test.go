package consumption

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/types"
)

// payloadMap decodes one task spec's payload for the field assertions below.
func payloadMap(t *testing.T, payload json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("unmarshal payload %s: %v", payload, err)
	}
	return m
}

func TestIngestTaskSpecsWithoutThumbnails(t *testing.T) {
	file := IngestFile{Path: "/inbox/report.pdf", Index: 3, MD5: "abc123", DocumentID: "doc-1"}
	specs := IngestTaskSpecs(file, false)

	if len(specs) != 2 {
		t.Fatalf("got %d specs, want consume plus enrich only", len(specs))
	}

	consume, enrich := specs[0], specs[1]
	if consume.Type != types.Task.Type.Consume || consume.Status != types.Task.Status.Pending {
		t.Errorf("first spec = %s/%s, want a pending consume task", consume.Type, consume.Status)
	}
	if consume.TaskID == "" {
		t.Error("consume spec must carry an explicit id so the children can link to it")
	}

	consumePayload := payloadMap(t, consume.Payload)
	if consumePayload["file_path"] != file.Path || consumePayload["md5"] != file.MD5 || consumePayload["document_id"] != file.DocumentID {
		t.Errorf("consume payload = %v, want the file identity fields", consumePayload)
	}
	if consumePayload["on_completed"] != enrich.TaskID {
		t.Errorf("on_completed = %v, want the enrich task id %q", consumePayload["on_completed"], enrich.TaskID)
	}
	if _, ok := consumePayload["on_completed_thumbnail"]; ok {
		t.Errorf("on_completed_thumbnail present without thumbnails: %v", consumePayload)
	}

	if enrich.Type != types.Task.Type.Enrich || enrich.Status != types.Task.Status.Waiting {
		t.Errorf("second spec = %s/%s, want a waiting enrich task", enrich.Type, enrich.Status)
	}
	if enrich.ParentTaskID != consume.TaskID {
		t.Errorf("enrich parent = %q, want %q", enrich.ParentTaskID, consume.TaskID)
	}
	enrichPayload := payloadMap(t, enrich.Payload)
	if enrichPayload["waiting_for"] != consume.TaskID {
		t.Errorf("waiting_for = %v, want the consume task id", enrichPayload["waiting_for"])
	}
	if enrichPayload["document_id"] != file.DocumentID {
		t.Errorf("enrich document_id = %v, want %q", enrichPayload["document_id"], file.DocumentID)
	}
	if enrichPayload["file_name"] != "report.pdf" {
		t.Errorf("enrich file_name = %v, want the path's base name", enrichPayload["file_name"])
	}
}

func TestIngestTaskSpecsWithThumbnails(t *testing.T) {
	file := IngestFile{Path: "/inbox/report.pdf", Index: 1, MD5: "abc123", DocumentID: "doc-1"}
	specs := IngestTaskSpecs(file, true)

	if len(specs) != 3 {
		t.Fatalf("got %d specs, want consume, enrich and thumbnail", len(specs))
	}

	consume, thumbnail := specs[0], specs[2]
	consumePayload := payloadMap(t, consume.Payload)
	if consumePayload["on_completed_thumbnail"] != thumbnail.TaskID {
		t.Errorf("on_completed_thumbnail = %v, want the thumbnail task id %q", consumePayload["on_completed_thumbnail"], thumbnail.TaskID)
	}
	if thumbnail.Type != types.Task.Type.Thumbnail || thumbnail.Status != types.Task.Status.Waiting {
		t.Errorf("third spec = %s/%s, want a waiting thumbnail task", thumbnail.Type, thumbnail.Status)
	}
	if thumbnail.ParentTaskID != consume.TaskID {
		t.Errorf("thumbnail parent = %q, want %q", thumbnail.ParentTaskID, consume.TaskID)
	}
	thumbnailPayload := payloadMap(t, thumbnail.Payload)
	if thumbnailPayload["waiting_for"] != consume.TaskID {
		t.Errorf("waiting_for = %v, want the consume task id", thumbnailPayload["waiting_for"])
	}
}
