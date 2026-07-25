//go:build cgo

package adapters

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// minimalPDF builds a syntactically valid single-page PDF with the given
// MediaBox dimensions (in points) and no content stream, computing correct
// xref byte offsets. Used to exercise the page-dimension guard in
// mupdf_render_page without depending on any on-disk fixture.
func minimalPDF(t *testing.T, width, height int) []byte {
	t.Helper()

	var buf bytes.Buffer
	offsets := make([]int, 5)

	buf.WriteString("%PDF-1.4\n")

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Contents 4 0 R /Resources << >> >>", width, height),
		"<< /Length 0 >>\nstream\nendstream",
	}

	for i, body := range objects {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}

	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 5\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Size 5 /Root 1 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF", xrefOffset)

	return buf.Bytes()
}

func writeTempPDF(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write temp pdf: %v", err)
	}
	return path
}

func TestRenderPage_NormalSizeSucceeds(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	path := writeTempPDF(t, minimalPDF(t, 612, 792)) // US Letter
	doc, err := ctx.OpenMuDocument(path)
	if err != nil {
		t.Fatalf("OpenMuDocument: %v", err)
	}
	defer doc.Close(ctx)

	w, h, samples, pixmap, err := doc.RenderPage(ctx, 0, 200)
	if err != nil {
		t.Fatalf("RenderPage: unexpected error for a normal-size page: %v", err)
	}
	defer FreePixmap(ctx, pixmap)

	if w <= 0 || h <= 0 || len(samples) == 0 {
		t.Fatalf("RenderPage returned empty result: w=%d h=%d len(samples)=%d", w, h, len(samples))
	}
}

func TestRenderPage_RejectsOversizedPage(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	// 4000x4000pt (~55.6x55.6in) renders to ~123M pixels at 200 DPI —
	// comfortably over the 100M-pixel render guard in mupdf_render_page.
	path := writeTempPDF(t, minimalPDF(t, 4000, 4000))
	doc, err := ctx.OpenMuDocument(path)
	if err != nil {
		t.Fatalf("OpenMuDocument: %v", err)
	}
	defer doc.Close(ctx)

	_, _, _, pixmap, err := doc.RenderPage(ctx, 0, 200)
	if err == nil {
		FreePixmap(ctx, pixmap)
		t.Fatal("expected RenderPage to reject an oversized page, got nil error")
	}
}
