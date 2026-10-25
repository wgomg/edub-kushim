package consume

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// stubHandler records how many steps reached it and answers every call with an empty result.
type stubHandler struct {
	calls int
}

func (h *stubHandler) Handle(_ context.Context, _ task.Task) (json.RawMessage, error) {
	h.calls++
	return json.RawMessage(`{}`), nil
}

// captureStdout runs fn and returns everything it printed, restoring os.Stdout even when fn panics.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured output: %v", err)
	}
	return string(out)
}

func payloadTask(taskType types.TaskType, payload string) database.ListBatchTaskProgressRow {
	raw := json.RawMessage(payload)
	return database.ListBatchTaskProgressRow{TaskType: taskType, Payload: &raw}
}

func TestTotalFiles(t *testing.T) {
	cases := []struct {
		name  string
		tasks []database.ListBatchTaskProgressRow
		want  int
	}{
		{
			name: "consume tasks report their highest file index",
			tasks: []database.ListBatchTaskProgressRow{
				payloadTask(types.Task.Type.Consume, `{"file_index":1}`),
				payloadTask(types.Task.Type.Consume, `{"file_index":4}`),
				payloadTask(types.Task.Type.Enrich, `{"file_index":9}`),
			},
			want: 4,
		},
		{
			name: "consume tasks without an index are counted",
			tasks: []database.ListBatchTaskProgressRow{
				payloadTask(types.Task.Type.Consume, `{"file_name":"a.pdf"}`),
				payloadTask(types.Task.Type.Consume, `{"file_name":"b.pdf"}`),
			},
			want: 2,
		},
		{
			name: "non-consume tasks are ignored",
			tasks: []database.ListBatchTaskProgressRow{
				payloadTask(types.Task.Type.Enrich, `{"file_name":"a.pdf"}`),
			},
			want: 0,
		},
		{
			name:  "no tasks counts nothing",
			tasks: nil,
			want:  0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := totalFiles(c.tasks); got != c.want {
				t.Errorf("totalFiles() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestParseExecToken pins the executor contract: the token is required and adopted unchanged, and a
// malformed token is refused loudly. The bare selector is no longer a self-claim path.
func TestParseExecToken(t *testing.T) {
	t.Run("an empty value is refused", func(t *testing.T) {
		if _, err := parseExecToken(""); err == nil {
			t.Fatal("an empty exec token must be refused")
		}
	})

	t.Run("a dispatcher token is adopted unchanged", func(t *testing.T) {
		want := uuid.New()
		token, err := parseExecToken(want.String())
		if err != nil {
			t.Fatalf("parseExecToken(%s): %v", want, err)
		}
		if token != want {
			t.Fatalf("token = %s, want %s", token, want)
		}
	})

	t.Run("a malformed token is refused", func(t *testing.T) {
		if _, err := parseExecToken("not-a-uuid"); err == nil {
			t.Fatal("a malformed token must be refused")
		}
	})
}

// TestDirectRunSummaryCountsFailedSteps pins the direct-run summary arithmetic: every failure lands inside
// a counted step, so the printed successful count cannot go negative when a step fails before any handler
// runs, and successful runs count each attempted step once.
func TestDirectRunSummaryCountsFailedSteps(t *testing.T) {
	ctx := context.Background()

	t.Run("a file that cannot be checksummed counts as one failed step", func(t *testing.T) {
		handler := &stubHandler{}
		run := &directRun{consume: handler}
		files := []consumption.File{{Name: "gone.pdf", OriginalPath: filepath.Join(t.TempDir(), "gone.pdf")}}

		var runErr error
		out := captureStdout(t, func() { runErr = run.process(ctx, files) })
		if runErr != nil {
			t.Fatalf("process reports and continues: %v", runErr)
		}
		if handler.calls != 0 {
			t.Fatalf("handler ran %d time(s) for an unhashable file, want 0", handler.calls)
		}
		want := "Summary: 1 files, 1 tasks — 0 successful, 1 failed"
		if !strings.Contains(out, want) {
			t.Fatalf("summary %q not found in output:\n%s", want, out)
		}
	})

	t.Run("successful steps are counted once each", func(t *testing.T) {
		handler := &stubHandler{}
		run := &directRun{consume: handler, enrich: handler}
		path := filepath.Join(t.TempDir(), "doc.pdf")
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		files := []consumption.File{{Name: "doc.pdf", OriginalPath: path}}

		var runErr error
		out := captureStdout(t, func() { runErr = run.process(ctx, files) })
		if runErr != nil {
			t.Fatalf("process: %v", runErr)
		}
		if handler.calls != 2 {
			t.Fatalf("handler ran %d time(s), want 2 (consume then enrich)", handler.calls)
		}
		want := "1 files, 2 tasks — all successful"
		if !strings.Contains(out, want) {
			t.Fatalf("summary %q not found in output:\n%s", want, out)
		}
	})
}
