//go:build cgo

package cgo

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

func openTestDocument(t *testing.T, ctx *MuContext, data []byte) *MuDocument {
	t.Helper()
	path := writeTempPDF(t, data)
	doc, err := ctx.OpenMuDocument(path)
	if err != nil {
		t.Fatalf("OpenMuDocument: %v", err)
	}
	return doc
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

func TestPageSizePixels_MatchesRenderedSize(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	doc := openTestDocument(t, ctx, minimalPDF(t, 612, 792)) // US Letter
	defer doc.Close(ctx)

	w, h, err := doc.PageSizePixels(ctx, 0, 200)
	if err != nil {
		t.Fatalf("PageSizePixels: unexpected error for a normal-size page: %v", err)
	}

	// 612x792pt at 200 DPI: 1700x2200 px, so the no-render probe must agree
	// with what RenderPage reports for the same page.
	if w != 1700 || h != 2200 {
		t.Fatalf("PageSizePixels = %dx%d, want 1700x2200", w, h)
	}

	rw, rh, _, pixmap, err := doc.RenderPage(ctx, 0, 200)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	defer FreePixmap(ctx, pixmap)

	if rw != w || rh != h {
		t.Fatalf("probe/render size mismatch: PageSizePixels=%dx%d, RenderPage=%dx%d", w, h, rw, rh)
	}
}

func TestPageSizePixels_RejectsOversizedPage(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	doc := openTestDocument(t, ctx, minimalPDF(t, 4000, 4000))
	defer doc.Close(ctx)

	// the page-level guard moves here so oversized pages fail before any
	// buffer exists; tiles never take the full-page render path.
	if _, _, err := doc.PageSizePixels(ctx, 0, 200); err == nil {
		t.Fatal("expected PageSizePixels to reject an oversized page, got nil error")
	}
}

func TestRenderTile_WholePageMatchesRenderPage(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	doc := openTestDocument(t, ctx, minimalPDF(t, 612, 792)) // US Letter
	defer doc.Close(ctx)

	// a tile covering the whole page must render the same pixels as the
	// full-page path at the same origin.
	_, _, full, fullPixmap, err := doc.RenderPage(ctx, 0, 200)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	defer FreePixmap(ctx, fullPixmap)

	samples, w, h, ox, oy, err := doc.RenderTile(ctx, 0, 200, 0, 0, 1700, 2200)
	if err != nil {
		t.Fatalf("RenderTile: %v", err)
	}

	if ox != 0 || oy != 0 || w != 1700 || h != 2200 {
		t.Fatalf("RenderTile = %dx%d at (%d,%d), want 1700x2200 at (0,0)", w, h, ox, oy)
	}
	if !bytes.Equal(samples, full) {
		t.Fatal("whole-page tile renders different pixels than RenderPage")
	}
}

func TestRenderTile_PartialTileMatchesSubrectangle(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	doc := openTestDocument(t, ctx, minimalPDF(t, 612, 792)) // US Letter
	defer doc.Close(ctx)

	// pixels of a page-region tile must match that region of the full render.
	_, _, full, fullPixmap, err := doc.RenderPage(ctx, 0, 200)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	defer FreePixmap(ctx, fullPixmap)

	const x0, y0, w, h = 100, 200, 300, 400
	samples, tw, th, ox, oy, err := doc.RenderTile(ctx, 0, 200, x0, y0, x0+w, y0+h)
	if err != nil {
		t.Fatalf("RenderTile: %v", err)
	}
	if ox != x0 || oy != y0 || tw != w || th != h {
		t.Fatalf("RenderTile = %dx%d at (%d,%d), want %dx%d at (%d,%d)", tw, th, ox, oy, w, h, x0, y0)
	}
	if len(samples) != w*h*3 {
		t.Fatalf("len(samples) = %d, want %d", len(samples), w*h*3)
	}

	for y := range h {
		want := full[((y0+y)*1700+x0)*3 : ((y0+y)*1700+x0+w)*3]
		got := samples[y*w*3 : (y+1)*w*3]
		if !bytes.Equal(got, want) {
			t.Fatalf("row %d of tile differs from the matching full-render subrectangle", y)
		}
	}
}

func TestRenderTile_ClampsToPageBounds(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	doc := openTestDocument(t, ctx, minimalPDF(t, 612, 792)) // US Letter, 1700x2200 px at 200 DPI
	defer doc.Close(ctx)

	// a tile hanging past the bottom-right corner is clipped to the page
	// instead of allocating outside it; the origin reports the actual pixmap.
	samples, w, h, ox, oy, err := doc.RenderTile(ctx, 0, 200, 1650, 2150, 1800, 2300)
	if err != nil {
		t.Fatalf("RenderTile: %v", err)
	}
	if ox != 1650 || oy != 2150 || w != 50 || h != 50 {
		t.Fatalf("RenderTile = %dx%d at (%d,%d), want 50x50 at (1650,2150)", w, h, ox, oy)
	}
	if len(samples) != 50*50*3 {
		t.Fatalf("len(samples) = %d, want %d", len(samples), 50*50*3)
	}
}

func TestRenderTile_RejectsEmptyIntersection(t *testing.T) {
	ctx, err := NewMuContext()
	if err != nil {
		t.Fatalf("NewMuContext: %v", err)
	}
	defer ctx.Close()

	doc := openTestDocument(t, ctx, minimalPDF(t, 612, 792)) // US Letter
	defer doc.Close(ctx)

	// a tile entirely outside the page must fail instead of allocating.
	if _, _, _, _, _, err := doc.RenderTile(ctx, 0, 200, 5000, 5000, 5100, 5100); err == nil {
		t.Fatal("expected RenderTile to reject a tile outside the page, got nil error")
	}
}
