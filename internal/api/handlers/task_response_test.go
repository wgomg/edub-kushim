package handlers

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	itypes "github.com/wgomg/edub-kushim/internal/types"
)

func rawPayload(t *testing.T, v any) *json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw := json.RawMessage(b)
	return &raw
}

func TestTaskToResponse_Result(t *testing.T) {
	resultJSON := json.RawMessage(`{"report_only":true,"worklist":{"stale":5}}`)

	t.Run("list response omits result", func(t *testing.T) {
		got := taskToResponse(database.Task{
			TaskType: itypes.Task.Type.TagReconcile,
			Status:   itypes.Task.Status.Completed,
			Result:   &resultJSON,
		})
		if got.Result != nil {
			t.Fatalf("expected nil Result in list response, got %v", got.Result)
		}
	})

	t.Run("detail response includes tagreconcile result", func(t *testing.T) {
		got := taskDetailResponse(database.Task{
			TaskType: itypes.Task.Type.TagReconcile,
			Status:   itypes.Task.Status.Completed,
			Result:   &resultJSON,
		})
		if got.Result == nil {
			t.Fatal("expected non-nil Result")
		}
		var report struct {
			ReportOnly bool `json:"report_only"`
		}
		if err := json.Unmarshal(got.Result, &report); err != nil {
			t.Fatalf("unmarshal Result: %v", err)
		}
		if !report.ReportOnly {
			t.Fatal("result.report_only = false, want true")
		}
	})

	t.Run("detail response omits result of other task types", func(t *testing.T) {
		got := taskDetailResponse(database.Task{
			TaskType: itypes.Task.Type.Consume,
			Status:   itypes.Task.Status.Completed,
			Result:   &resultJSON,
		})
		if got.Result != nil {
			t.Fatalf("expected nil Result for consume task, got %v", got.Result)
		}
	})

	t.Run("nil result yields omitted field", func(t *testing.T) {
		got := taskDetailResponse(database.Task{
			TaskType: itypes.Task.Type.TagReconcile,
			Status:   itypes.Task.Status.Completed,
		})
		if got.Result != nil {
			t.Fatalf("expected nil Result, got %v", got.Result)
		}
	})
}

func TestTaskToResponse_PayloadDocID(t *testing.T) {
	const docUUID = "f0e1d2c3-b4a5-4687-8901-23456789abcd"

	tests := []struct {
		name string
		task database.Task
		want string
	}{
		{
			// The original motivation: failed thumbnail tasks now expose the
			// document so the UI can link to it.
			name: "failed thumbnail with document_id surfaces it",
			task: database.Task{
				TaskType: itypes.Task.Type.Thumbnail,
				Status:   itypes.Task.Status.Failed,
				Payload:  rawPayload(t, map[string]string{"document_id": docUUID}),
			},
			want: docUUID,
		},
		{
			// Behaviour change: discarded enrich/thumbnail children no longer
			// mislabel waiting_for (a parent task id) as a document id.
			name: "discarded enrich with only waiting_for yields empty payload_doc_id",
			task: database.Task{
				TaskType: itypes.Task.Type.Enrich,
				Status:   itypes.Task.Status.Discarded,
				Payload:  rawPayload(t, map[string]string{"waiting_for": "parent-task-id"}),
			},
			want: "",
		},
		{
			// Consume tasks that have not yet created the document record
			// carry an empty document_id, so no link is shown.
			name: "consume payload without document_id yields empty payload_doc_id",
			task: database.Task{
				TaskType: itypes.Task.Type.Consume,
				Status:   itypes.Task.Status.Processing,
				Payload:  rawPayload(t, map[string]string{"file_path": "/tmp/inbox/a.pdf"}),
			},
			want: "",
		},
		{
			// File-name extraction from file_path remains intact alongside
			// the payload_doc_id change.
			name: "consume payload with document_id and file_path sets payload_doc_id",
			task: database.Task{
				TaskType: itypes.Task.Type.Consume,
				Status:   itypes.Task.Status.Completed,
				Payload: rawPayload(t, map[string]string{
					"file_path":   "/tmp/inbox/2026/01/01/foo.pdf",
					"document_id": docUUID,
				}),
			},
			want: docUUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := taskToResponse(tt.task)
			if got.PayloadDocID != tt.want {
				t.Fatalf("taskToResponse().PayloadDocID = %q, want %q", got.PayloadDocID, tt.want)
			}
		})
	}
}
