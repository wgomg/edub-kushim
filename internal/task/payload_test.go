package task

import (
	"encoding/json"
	"testing"
)

func rawJSON(s string) *json.RawMessage {
	r := json.RawMessage(s)
	return &r
}

func TestReadPayload(t *testing.T) {
	t.Run("nil payload yields the zero value", func(t *testing.T) {
		if p := ReadPayload(nil); p != (TaskPayload{}) {
			t.Errorf("ReadPayload(nil) = %+v, want the zero value", p)
		}
	})

	t.Run("malformed payload yields the zero value", func(t *testing.T) {
		if p := ReadPayload(rawJSON("{not json")); p != (TaskPayload{}) {
			t.Errorf("ReadPayload(malformed) = %+v, want the zero value", p)
		}
	})

	t.Run("decodes the display fields", func(t *testing.T) {
		got := ReadPayload(rawJSON(`{"file_path":"/inbox/a.pdf","file_name":"a.pdf","file_index":3,"document_id":"doc-1"}`))
		want := TaskPayload{FilePath: "/inbox/a.pdf", FileName: "a.pdf", FileIndex: 3, DocumentID: "doc-1"}
		if got != want {
			t.Errorf("ReadPayload = %+v, want %+v", got, want)
		}
	})
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		name    string
		payload TaskPayload
		want    string
	}{
		{"file_path wins as its base name", TaskPayload{FilePath: "/inbox/report.pdf", FileName: "renamed.pdf"}, "report.pdf"},
		{"falls back to file_name", TaskPayload{FileName: "only-name.pdf"}, "only-name.pdf"},
		{"empty when neither field is set", TaskPayload{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.payload.DisplayName(); got != c.want {
				t.Errorf("DisplayName() = %q, want %q", got, c.want)
			}
		})
	}
}
