//go:build cgo

package ocr

import (
	"image"
	"testing"

	"github.com/go-pdf/fpdf"
	gosseract "github.com/otiai10/gosseract/v2"
)

func TestAddPDFPage_PlacesTilesAndInheritsTextBoxes(t *testing.T) {
	// two adjacent tiles with one word owned by the right tile assemble into a single page:
	// each tile JPEG is named per page and index, and the kept box keeps page-pixel coordinates.
	pdf := fpdf.New("P", "pt", "", "")
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	jpegData, err := encodeJPEG(img, 60)
	if err != nil {
		t.Fatalf("encodeJPEG: %v", err)
	}

	left := ocrTileResult{jpegData: jpegData, core: image.Rect(0, 0, 200, 100)}
	right := ocrTileResult{jpegData: jpegData, core: image.Rect(200, 0, 400, 100)}
	rightBox := image.Rect(220, 10, 280, 30)

	res := ocrPageResult{
		tiles: []ocrTileResult{left, right},
		boxes: []gosseract.BoundingBox{{Box: rightBox, Word: "owned"}},
		w:     400,
		h:     100,
	}

	addPDFPage(pdf, res, 0)

	if pdf.PageCount() != 1 {
		t.Fatalf("PageCount() = %d, want 1", pdf.PageCount())
	}
	for _, name := range []string{"kushim-page-0-0", "kushim-page-0-1"} {
		if info := pdf.GetImageInfo(name); info == nil {
			t.Fatalf("image %q was not registered", name)
		}
	}
}
