package ocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"

	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	gosseract "github.com/otiai10/gosseract/v2"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// Gosseract implements OCR using Tesseract (gosseract) with MuPDF for page
// rendering via the custom CGo wrapper (mupdf_wrapper.go).
type Gosseract struct {
	logger    *utils.Logger
	config    config.ToolConfig
	optimizer pdfoptimizer.PdfOptimizer

	languages []string
	dataDir   string
}

func NewGosseract(logger *utils.Logger, cfg config.ToolConfig, optimizerCmd string, languages []string, dataDir string) (*Gosseract, error) {
	optCfg := config.ToolConfig{Command: optimizerCmd, Timeout: cfg.Timeout}
	optimizer, err := pdfoptimizer.NewPdfOptimizer(logger, optCfg)
	if err != nil {
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	return &Gosseract{logger: logger, config: cfg, optimizer: optimizer, languages: languages, dataDir: dataDir}, nil
}

func (o *Gosseract) Process(ctx context.Context, path string) (*string, error) {
	if err := EnsureLanguages(o.logger, o.dataDir, o.languages); err != nil {
		return nil, fmt.Errorf("tessdata setup: %w", err)
	}

	mupdfCtx, err := adapters.NewMuContext()
	if err != nil {
		return nil, fmt.Errorf("mupdf context: %w", err)
	}
	defer mupdfCtx.Close()

	doc, err := mupdfCtx.OpenMuDocument(path)
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	defer doc.Close(mupdfCtx)

	numPages := doc.NumPages(mupdfCtx)
	if numPages == 0 {
		return nil, fmt.Errorf("PDF has no pages")
	}

	pdf := fpdf.New("P", "pt", "", "")
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)

	client := gosseract.NewClient()
	defer client.Close()
	client.SetLanguage(LangString(o.languages))
	client.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK)
	client.DisableOutput()

	o.logger.Info(nil, "starting OCR on %d pages", numPages)

	const ocrDPI = 200
	const outputDPI = 150

	for i := range numPages {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if i > 0 && i%50 == 0 {
			o.logger.Info(nil, "OCR page %d/%d", i, numPages)
		}

		// Render page at 200 DPI for OCR
		ocrW, ocrH, ocrSamples, ocrPixmap, err := doc.RenderPage(mupdfCtx, i, ocrDPI)
		if err != nil {
			return nil, fmt.Errorf("page %d: render error: %w", i, err)
		}
		ocrImg := samplesToRGBA(ocrSamples, ocrW, ocrH)
		adapters.FreePixmap(mupdfCtx, ocrPixmap)

		pngData, err := encodePNG(ocrImg)
		ocrImg = nil
		if err != nil {
			return nil, fmt.Errorf("page %d: PNG encode error: %w", i, err)
		}

		client.SetImageFromBytes(pngData)
		boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)
		pngData = nil
		if err != nil {
			return nil, fmt.Errorf("page %d: OCR error: %w", i, err)
		}

		// Render again at lower DPI for the output image
		lowW, lowH, lowSamples, lowPixmap, err := doc.RenderPage(mupdfCtx, i, outputDPI)
		if err != nil {
			return nil, fmt.Errorf("page %d: low-res render error: %w", i, err)
		}
		lowImg := samplesToRGBA(lowSamples, lowW, lowH)
		adapters.FreePixmap(mupdfCtx, lowPixmap)

		pageW := float64(lowW) * 72.0 / outputDPI
		pageH := float64(lowH) * 72.0 / outputDPI

		pdf.AddPageFormat("P", fpdf.SizeType{Wd: pageW, Ht: pageH})

		jpegData, err := encodeJPEG(lowImg, 60)
		lowImg = nil
		if err != nil {
			return nil, fmt.Errorf("page %d: JPEG encode error: %w", i, err)
		}
		imgName := fmt.Sprintf("kushim-page-%d", i)
		pdf.RegisterImageReader(imgName, "jpg", bytes.NewReader(jpegData))
		pdf.Image(imgName, 0, 0, pageW, pageH, false, "", 0, "")
		jpegData = nil

		// Overlay invisible text for searchability (text rendering mode 3)
		if len(boxes) == 0 {
			o.logger.Debug(nil, "page %d: no text recognized", i)
		}
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

	outPath := filepath.Join(os.TempDir(), "ocr_"+uuid.New().String()+".pdf")
	err = pdf.OutputFileAndClose(outPath)
	if err != nil {
		return nil, fmt.Errorf("save output PDF: %w", err)
	}

	o.logger.Info(nil, "created searchable PDF: %s", outPath)

	optResult, err := o.optimizer.Optimize(ctx, outPath)
	if err != nil {
		os.Remove(outPath)
		return nil, fmt.Errorf("optimize OCR output: %w", err)
	}
	os.Remove(outPath)
	o.logger.Debug(nil, "optimized OCR output: %s -> %s", outPath, *optResult)
	return optResult, nil
}

func (o *Gosseract) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (o *Gosseract) Name() string {
	return "gosseract"
}

// samplesToRGBA converts raw RGB samples (width * height * 3 bytes) to image.RGBA.
func samplesToRGBA(samples []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			offset := (y*w + x) * 3
			i := img.PixOffset(x, y)
			img.Pix[i] = samples[offset]     // R
			img.Pix[i+1] = samples[offset+1] // G
			img.Pix[i+2] = samples[offset+2] // B
			img.Pix[i+3] = 255               // A
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
