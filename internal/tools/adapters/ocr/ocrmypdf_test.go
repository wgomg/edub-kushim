package ocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestOcrMyPdf_Process(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(src, imageBasedPDF(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ocr, err := NewOcrMyPdf(utils.NewDiscardLogger(), config.ToolConfig{Command: "ocrmypdf", Timeout: 60})
	if err != nil {
		t.Fatalf("ocrmypdf not available: %v", err)
	}

	out, err := ocr.Process(context.Background(), src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if out == nil || *out == "" {
		t.Fatal("expected non-empty output path")
	}
	defer os.Remove(*out)

	if _, err := os.Stat(*out); os.IsNotExist(err) {
		t.Fatal("output file does not exist")
	}
}

func TestOcrMyPdf_CanHandle(t *testing.T) {
	ocr, err := NewOcrMyPdf(utils.NewDiscardLogger(), config.ToolConfig{Command: "ocrmypdf", Timeout: 30})
	if err != nil {
		t.Fatalf("ocrmypdf not available: %v", err)
	}
	if !ocr.CanHandle("application/pdf") {
		t.Error("CanHandle(application/pdf) = false")
	}
	if ocr.CanHandle("image/png") {
		t.Error("CanHandle(image/png) = true")
	}
}

func TestOcrMyPdf_Name(t *testing.T) {
	ocr, err := NewOcrMyPdf(utils.NewDiscardLogger(), config.ToolConfig{Command: "ocrmypdf", Timeout: 30})
	if err != nil {
		t.Fatalf("ocrmypdf not available: %v", err)
	}
	if ocr.Name() != "ocrmypdf" {
		t.Errorf("Name() = %q, want ocrmypdf", ocr.Name())
	}
}

func imageBasedPDF() []byte {
	img := image.NewGray(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.Gray{Y: uint8((x + y) % 256)})
		}
	}
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, img, &jpeg.Options{Quality: 85}); err != nil {
		panic(err)
	}
	jpgData := jpgBuf.Bytes()

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int64, 0, 8)
	offsets = append(offsets, int64(pdf.Len()))
	pdf.WriteString("1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n")
	offsets = append(offsets, int64(pdf.Len()))
	pdf.WriteString("2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n")
	offsets = append(offsets, int64(pdf.Len()))
	pdf.WriteString("3 0 obj<</Type/Page/MediaBox[0 0 100 100]/Parent 2 0 R/Resources<</XObject<</Im0 4 0 R>>>>/Contents 5 0 R>>endobj\n")
	offsets = append(offsets, int64(pdf.Len()))
	pdf.WriteString(fmt.Sprintf("4 0 obj<</Type/XObject/Subtype/Image/Width 100/Height 100/ColorSpace/DeviceGray/BitsPerComponent 8/Filter/DCTDecode/Length %d>>stream\n", len(jpgData)))
	pdf.Write(jpgData)
	pdf.WriteString("\nendstream\nendobj\n")
	offsets = append(offsets, int64(pdf.Len()))
	pdf.WriteString("5 0 obj<</Length 44>>stream\nq 100 0 0 100 0 0 cm /Im0 Do Q\nendstream\nendobj\n")
	xrefOffset := int64(pdf.Len())
	pdf.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, off := range offsets {
		pdf.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	pdf.WriteString(fmt.Sprintf("trailer<</Size 6/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF", xrefOffset))
	return pdf.Bytes()
}
