package ocr

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"

	"github.com/gen2brain/go-fitz"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	gosseract "github.com/otiai10/gosseract/v2"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/utils"
)

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

func (o *Gosseract) Process(path string) (*string, error) {
	if err := EnsureLanguages(o.logger, o.dataDir, o.languages); err != nil {
		return nil, fmt.Errorf("tessdata setup: %w", err)
	}

	doc, err := fitz.New(path)
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	defer doc.Close()

	numPages := doc.NumPage()
	if numPages == 0 {
		return nil, fmt.Errorf("PDF has no pages")
	}

	pdf := fpdf.New("P", "pt", "A4", "")
	pdf.SetAutoPageBreak(false, 0)

	pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)

	client := gosseract.NewClient()
	defer client.Close()
	client.SetLanguage(LangString(o.languages))

	for i := range numPages {
		bnd, err := doc.Bound(i)
		if err != nil {
			return nil, fmt.Errorf("page %d: bound error: %w", i, err)
		}
		pageW := float64(bnd.Dx())
		pageH := float64(bnd.Dy())

		// Render page to image at 300 DPI (doc.Image defaults to 300)
		img, err := doc.Image(i)
		if err != nil {
			return nil, fmt.Errorf("page %d: render error: %w", i, err)
		}

		// PPM‑encode for OCR (avoids libjpeg; Leptonica handles PPM natively)
		ppmData, err := encodePPM(img)
		if err != nil {
			return nil, fmt.Errorf("page %d: PPM encode error: %w", i, err)
		}

		client.SetImageFromBytes(ppmData)
		boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)
		if err != nil {
			return nil, fmt.Errorf("page %d: OCR error: %w", i, err)
		}

		pdf.AddPageFormat("P", fpdf.SizeType{Wd: pageW, Ht: pageH})

		jpegData, err := encodeJPEG(img, 85)
		if err != nil {
			return nil, fmt.Errorf("page %d: JPEG encode error: %w", i, err)
		}
		imgName := fmt.Sprintf("kushim-page-%d", i)
		pdf.RegisterImageReader(imgName, "jpg", bytes.NewReader(jpegData))
		pdf.Image(imgName, 0, 0, pageW, pageH, false, "", 0, "")

		// Overlay invisible text for searchability (text rendering mode 3)
		// Tesseract boxes are in image pixel space (300 DPI); convert to PDF
		// point space (72 DPI) and flip Y from top-left → bottom-left.
		if len(boxes) == 0 {
			o.logger.Debug(nil, "page %d: no text recognized", i)
		}
		imgW := float64(img.Bounds().Dx())
		imgH := float64(img.Bounds().Dy())
		scaleX := pageW / imgW
		scaleY := pageH / imgH

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
			top := float64(box.Box.Max.Y) * scaleY // fpdf top-left origin
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

	// Optimize the raw fpdf output before returning. Gosseract produces
	// full-resolution JPEG images; this matches the OCR.Process() contract
	// of returning a final-ready PDF (equivalent to ocrmypdf's internal
	// optimization).
	optResult, err := o.optimizer.Optimize(outPath)
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

// encodePPM converts an image.Image to PPM (P6 binary) bytes.
// PPM is handled natively by Leptonica without external libraries.
func encodePPM(img image.Image) ([]byte, error) {
	bnd := img.Bounds()
	w, h := bnd.Dx(), bnd.Dy()

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "P6\n%d %d\n255\n", w, h)

	for y := range h {
		for x := range w {
			r, g, b, _ := img.At(x+bnd.Min.X, y+bnd.Min.Y).RGBA()
			buf.WriteByte(byte(r >> 8))
			buf.WriteByte(byte(g >> 8))
			buf.WriteByte(byte(b >> 8))
		}
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
