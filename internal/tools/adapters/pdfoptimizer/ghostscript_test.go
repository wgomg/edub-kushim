package pdfoptimizer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestGhostscript_Optimize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")
	os.WriteFile(src, []byte(minimalPDF), 0644)

	opt, err := NewGhostscript(utils.NewDiscardLogger(), config.ToolConfig{Command: "gs", Timeout: 30})
	if err != nil {
		t.Fatalf("gs not available: %v", err)
	}

	out, err := opt.Optimize(context.Background(), src)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if out == nil || *out == "" {
		t.Fatal("expected non-empty output path")
	}
	defer os.Remove(*out)

	if _, err := os.Stat(*out); os.IsNotExist(err) {
		t.Fatal("output file does not exist")
	}
}

func TestGhostscript_Name(t *testing.T) {
	opt, err := NewGhostscript(utils.NewDiscardLogger(), config.ToolConfig{Command: "gs", Timeout: 30})
	if err != nil {
		t.Fatalf("gs not available: %v", err)
	}
	if opt.Name() != "ghostscript" {
		t.Errorf("Name() = %q, want ghostscript", opt.Name())
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
