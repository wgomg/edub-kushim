//go:build cgo

package thumbnail

import (
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"

	"github.com/gabriel-vasile/mimetype"
	_mime "github.com/wgomg/edub-kushim/internal/mime"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
	_ "golang.org/x/image/tiff"
)

func RunStandalone(inputPath, outputPath string, dpi, maxWidth, quality int) error {
	if maxWidth < 1 {
		return fmt.Errorf("max_width must be >= 1")
	}
	if quality < 1 || quality > 100 {
		return fmt.Errorf("quality must be between 1 and 100")
	}

	mtype, err := mimetype.DetectFile(inputPath)
	if err != nil {
		return fmt.Errorf("detect file type: %w", err)
	}

	samples, w, h, err := renderSource(inputPath, mtype.String(), dpi)
	if err != nil {
		return err
	}

	img, err := coverFit(samples, w, h, maxWidth, maxWidth*4/3)
	if err != nil {
		return err
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()
	if err := jpeg.Encode(out, img, &jpeg.Options{Quality: quality}); err != nil {
		return fmt.Errorf("JPEG encode: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%dx%d\n", img.Bounds().Dx(), img.Bounds().Dy())
	return nil
}

func renderSource(inputPath, mimeType string, dpi int) (samples []byte, w, h int, err error) {
	if _mime.IsImage(mimeType) {
		f, err := os.Open(inputPath)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("open image: %w", err)
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("decode image: %w", err)
		}

		bounds := img.Bounds()
		w = bounds.Dx()
		h = bounds.Dy()
		if w == 0 || h == 0 {
			return nil, 0, 0, fmt.Errorf("image has zero dimensions")
		}

		const maxPixels = 50_000_000
		if int64(w)*int64(h) > maxPixels {
			return nil, 0, 0, fmt.Errorf("image dimensions %dx%d exceed maximum %d pixels", w, h, maxPixels)
		}

		return imageToRGB(img), w, h, nil
	}

	if !_mime.IsPDF(mimeType) {
		return nil, 0, 0, fmt.Errorf("unsupported type %s — expected PDF or image", mimeType)
	}

	mupdfCtx, err := adapters.NewMuContext()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("mupdf context: %w", err)
	}
	defer mupdfCtx.Close()

	doc, err := mupdfCtx.OpenMuDocument(inputPath)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("open PDF: %w", err)
	}
	defer doc.Close(mupdfCtx)

	if doc.NumPages(mupdfCtx) == 0 {
		return nil, 0, 0, fmt.Errorf("PDF has no pages")
	}

	ocrW, ocrH, samples, pixmap, err := doc.RenderPage(mupdfCtx, 0, float64(dpi))
	adapters.FreePixmap(mupdfCtx, pixmap)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("render page 0: %w", err)
	}
	return samples, ocrW, ocrH, nil
}

// coverFit scales RGB samples so the smaller dimension fills the target box
// and crops the overflow, centered (object-fit: cover semantics). Returns an
// RGBA image exactly the size of the target box.
func coverFit(samples []byte, w, h, tw, th int) (*image.RGBA, error) {
	if tw < 1 || th < 1 {
		return nil, fmt.Errorf("target box must be >= 1x1")
	}
	if w < 1 || h < 1 || len(samples) < w*h*3 {
		return nil, fmt.Errorf("invalid source dimensions %dx%d", w, h)
	}

	scale := math.Max(float64(tw)/float64(w), float64(th)/float64(h))
	srcW := int(math.Ceil(float64(tw) / scale))
	srcH := int(math.Ceil(float64(th) / scale))
	if srcW < 1 || srcH < 1 {
		return nil, fmt.Errorf("source window is empty")
	}
	srcX := (w - srcW) / 2
	srcY := (h - srcH) / 2
	if srcX < 0 {
		srcX = 0
	}
	if srcY < 0 {
		srcY = 0
	}
	if srcX+srcW > w {
		srcW = w - srcX
	}
	if srcY+srcH > h {
		srcH = h - srcY
	}

	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	maxSi := (h*w - 1) * 3
	for y := range th {
		srcRow := srcY + y*srcH/th
		for x := range tw {
			si := (srcRow*w + srcX + x*srcW/tw) * 3
			if si+2 > maxSi || si+2 >= len(samples) {
				return nil, fmt.Errorf("cover-fit index out of range: %d", si)
			}
			di := (y*tw + x) * 4
			out.Pix[di] = samples[si]
			out.Pix[di+1] = samples[si+1]
			out.Pix[di+2] = samples[si+2]
			out.Pix[di+3] = 255
		}
	}
	return out, nil
}

func imageToRGB(img image.Image) []byte {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	samples := make([]byte, w*h*3)
	for y := range h {
		for x := range w {
			r, g, b, a := img.At(x+bounds.Min.X, y+bounds.Min.Y).RGBA()
			i := (y*w + x) * 3
			samples[i] = uint8((r + 65535 - a) >> 8)
			samples[i+1] = uint8((g + 65535 - a) >> 8)
			samples[i+2] = uint8((b + 65535 - a) >> 8)
		}
	}
	return samples
}
