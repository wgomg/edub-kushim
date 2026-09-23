package textextractor

import (
	"context"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type mockExtractor struct {
	handledMIME string
	name        string
	extracted   string
}

func (m *mockExtractor) Extract(_ context.Context, _ string, _ string) (*string, error) {
	return &m.extracted, nil
}

func (m *mockExtractor) CanHandle(mimeType string) bool {
	return mimeType == m.handledMIME
}

func (m *mockExtractor) Name() string {
	return m.name
}

func TestCompositeExtractor_Extract_DispatchesByMIME(t *testing.T) {
	pdf := &mockExtractor{handledMIME: "application/pdf", name: "pdf", extracted: "pdf text"}
	docx := &mockExtractor{handledMIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", name: "docx", extracted: "docx text"}
	odt := &mockExtractor{handledMIME: "application/vnd.oasis.opendocument.text", name: "odt", extracted: "odt text"}

	c := NewCompositeExtractor([]TextExtractor{pdf, docx, odt})
	ctx := context.Background()

	tests := []struct {
		name     string
		mime     string
		wantText string
	}{
		{"pdf dispatched", "application/pdf", "pdf text"},
		{"docx dispatched", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx text"},
		{"odt dispatched", "application/vnd.oasis.opendocument.text", "odt text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.Extract(ctx, "dummy-path", tt.mime)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("result is nil")
			}
			if *result != tt.wantText {
				t.Errorf("got %q, want %q", *result, tt.wantText)
			}
		})
	}
}

func TestCompositeExtractor_Extract_UnknownMIME(t *testing.T) {
	pdf := &mockExtractor{handledMIME: "application/pdf", name: "pdf"}
	c := NewCompositeExtractor([]TextExtractor{pdf})

	_, err := c.Extract(context.Background(), "dummy", "image/png")
	if err == nil {
		t.Fatal("expected error for unknown MIME type")
	}
}

func TestCompositeExtractor_CanHandle(t *testing.T) {
	pdf := &mockExtractor{handledMIME: "application/pdf", name: "pdf"}
	docx := &mockExtractor{handledMIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", name: "docx"}
	odt := &mockExtractor{handledMIME: "application/vnd.oasis.opendocument.text", name: "odt"}

	c := NewCompositeExtractor([]TextExtractor{pdf, docx, odt})

	tests := []struct {
		mime string
		want bool
	}{
		{"application/pdf", true},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", true},
		{"application/vnd.oasis.opendocument.text", true},
		{"image/png", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := c.CanHandle(tt.mime); got != tt.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestCompositeExtractor_Name(t *testing.T) {
	c := NewCompositeExtractor([]TextExtractor{&mockExtractor{}})
	if got := c.Name(); got != "composite" {
		t.Errorf("Name() = %q, want %q", got, "composite")
	}
}

func TestNewTextExtractor_ReturnsComposite(t *testing.T) {
	logger := utils.NewDiscardLogger()
	extractor, err := NewTextExtractor(logger, config.ToolConfig{Command: "gopdf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, ok := extractor.(*CompositeExtractor)
	if !ok {
		t.Fatalf("expected *CompositeExtractor, got %T", extractor)
	}
}

func TestNewTextExtractor_CompositeHandlesAllTypes(t *testing.T) {
	logger := utils.NewDiscardLogger()
	extractor, err := NewTextExtractor(logger, config.ToolConfig{Command: "gopdf"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mimes := []string{
		"application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.oasis.opendocument.text",
	}
	for _, mime := range mimes {
		if !extractor.CanHandle(mime) {
			t.Errorf("composite should handle %q", mime)
		}
	}
}
