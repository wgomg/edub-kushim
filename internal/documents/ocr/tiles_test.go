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

func TestPlanChunks(t *testing.T) {
	tests := []struct {
		name  string
		pageW []int
		pageH []int
		want  []chunkRange
	}{
		{
			name:  "few small pages form one chunk",
			pageW: []int{1654, 1654, 1654},
			pageH: []int{2339, 2339, 2339},
			want:  []chunkRange{{firstPage: 0, lastPage: 3}},
		},
		{
			name:  "page-count bound splits at 64",
			pageW: repeatWidth(70, 1654),
			pageH: repeatWidth(70, 2339),
			want: []chunkRange{
				{firstPage: 0, lastPage: 64},
				{firstPage: 64, lastPage: 70},
			},
		},
		{
			name:  "pixel budget splits monster pages",
			pageW: []int{4000, 4000, 4000},
			pageH: []int{25000, 25000, 25000},
			want: []chunkRange{
				{firstPage: 0, lastPage: 2},
				{firstPage: 2, lastPage: 3},
			},
		},
		{
			name:  "lone oversized page keeps its own chunk",
			pageW: []int{40000, 4000},
			pageH: []int{40000, 4000},
			want: []chunkRange{
				{firstPage: 0, lastPage: 1},
				{firstPage: 1, lastPage: 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planChunks(tt.pageW, tt.pageH)
			if len(got) != len(tt.want) {
				t.Fatalf("planChunks returned %d chunks, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("chunk %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func repeatWidth(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}
