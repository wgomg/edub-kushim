//go:build cgo

package ocr

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"

	"github.com/go-pdf/fpdf"
	gosseract "github.com/otiai10/gosseract/v2"

	"github.com/wgomg/edub-kushim/internal/tools/adapters"
)

func RunStandalone(inputPath, outputPath string, languages []string, dataDir string) error {
	mupdfCtx, err := adapters.NewMuContext()
	if err != nil {
		return fmt.Errorf("mupdf context: %w", err)
	}
	defer mupdfCtx.Close()

	doc, err := mupdfCtx.OpenMuDocument(inputPath)
	if err != nil {
		return fmt.Errorf("open PDF: %w", err)
	}
	defer doc.Close(mupdfCtx)

	numPages := doc.NumPages(mupdfCtx)
	if numPages == 0 {
		return fmt.Errorf("PDF has no pages")
	}

	pdf := fpdf.New("P", "pt", "", "")
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)

	client := gosseract.NewClient()
	defer client.Close()
	client.SetLanguage(LangString(languages))
	client.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK)
	client.DisableOutput()

	fmt.Fprintf(os.Stdout, "starting OCR on %d pages\n", numPages)

	const ocrDPI = 200
	const outputDPI = 150

	for i := range numPages {
		if i > 0 && i%50 == 0 {
			fmt.Fprintf(os.Stdout, "OCR page %d/%d\n", i+1, numPages)
		}
		ocrW, ocrH, ocrSamples, ocrPixmap, err := doc.RenderPage(mupdfCtx, i, ocrDPI)
		if err != nil {
			return fmt.Errorf("page %d: render error: %w", i, err)
		}
		ocrImg := samplesToRGBA(ocrSamples, ocrW, ocrH)
		adapters.FreePixmap(mupdfCtx, ocrPixmap)

		pngData, err := encodePNG(ocrImg)
		ocrImg = nil
		if err != nil {
			return fmt.Errorf("page %d: PNG encode error: %w", i, err)
		}

		client.SetImageFromBytes(pngData)
		boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)
		pngData = nil
		if err != nil {
			return fmt.Errorf("page %d: OCR error: %w", i, err)
		}

		lowW, lowH, lowSamples, lowPixmap, err := doc.RenderPage(mupdfCtx, i, outputDPI)
		if err != nil {
			return fmt.Errorf("page %d: low-res render error: %w", i, err)
		}
		lowImg := samplesToRGBA(lowSamples, lowW, lowH)
		adapters.FreePixmap(mupdfCtx, lowPixmap)

		pageW := float64(lowW) * 72.0 / outputDPI
		pageH := float64(lowH) * 72.0 / outputDPI

		pdf.AddPageFormat("P", fpdf.SizeType{Wd: pageW, Ht: pageH})

		jpegData, err := encodeJPEG(lowImg, 60)
		lowImg = nil
		if err != nil {
			return fmt.Errorf("page %d: JPEG encode error: %w", i, err)
		}
		imgName := fmt.Sprintf("kushim-page-%d", i)
		pdf.RegisterImageReader(imgName, "jpg", bytes.NewReader(jpegData))
		pdf.Image(imgName, 0, 0, pageW, pageH, false, "", 0, "")
		jpegData = nil

		scaleX := pageW / float64(ocrW)
		scaleY := pageH / float64(ocrH)

		pdf.SetTextRenderingMode(3)
		for _, box := range boxes {
			width := float64(box.Box.Dx()) * scaleX
			height := float64(box.Box.Dy()) * scaleY
			if width < 1 || height < 1 {
				continue
			}
			fontSize := height * 0.8
			if fontSize < 2 {
				fontSize = 2
			}
			left := float64(box.Box.Min.X) * scaleX
			top := float64(box.Box.Max.Y) * scaleY
			pdf.SetFont("KushimText", "", fontSize)
			pdf.Text(left, top, box.Word)
		}
	}

	if err := pdf.OutputFileAndClose(outputPath); err != nil {
		return fmt.Errorf("save output PDF: %w", err)
	}
	return nil
}

func samplesToRGBA(samples []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			offset := (y*w + x) * 3
			i := img.PixOffset(x, y)
			img.Pix[i] = samples[offset]
			img.Pix[i+1] = samples[offset+1]
			img.Pix[i+2] = samples[offset+2]
			img.Pix[i+3] = 255
		}
	}
	return img
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
