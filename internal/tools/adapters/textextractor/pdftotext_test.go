package textextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestPDFToText_Extract(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")
	os.WriteFile(src, []byte(minimalPDF), 0644)

	ext, err := NewPDFToText(utils.NewDiscardLogger(), config.ToolConfig{Command: "pdftotext", Timeout: 30})
	if err != nil {
		t.Fatalf("pdftotext not available: %v", err)
	}

	text, err := ext.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if text == nil || *text == "" {
		t.Fatal("expected non-empty text")
	}
}

func TestPDFToText_CanHandle(t *testing.T) {
	ext, err := NewPDFToText(utils.NewDiscardLogger(), config.ToolConfig{Command: "pdftotext", Timeout: 30})
	if err != nil {
		t.Fatalf("pdftotext not available: %v", err)
	}
	if !ext.CanHandle("application/pdf") {
		t.Error("CanHandle(application/pdf) = false")
	}
	if ext.CanHandle("image/png") {
		t.Error("CanHandle(image/png) = true")
	}
}

func TestPDFToText_Name(t *testing.T) {
	ext, err := NewPDFToText(utils.NewDiscardLogger(), config.ToolConfig{Command: "pdftotext", Timeout: 30})
	if err != nil {
		t.Fatalf("pdftotext not available: %v", err)
	}
	if ext.Name() != "pdftotext" {
		t.Errorf("Name() = %q, want pdftotext", ext.Name())
	}
}

const minimalPDF = `%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>endobj
4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj
5 0 obj<</Length 44>>stream
BT /F1 12 Tf 100 700 Td (Hello World) Tj ET
endstream
endobj
xref
0 6
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
0000000210 00000 n
0000000277 00000 n
trailer<</Size 6/Root 1 0 R>>
startxref
366
%%EOF`
