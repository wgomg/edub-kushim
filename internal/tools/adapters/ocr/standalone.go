//go:build cgo

package ocr

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/go-pdf/fpdf"
	"github.com/gabriel-vasile/mimetype"
	gosseract "github.com/otiai10/gosseract/v2"
	_ "golang.org/x/image/tiff"

	"github.com/wgomg/edub-kushim/internal/tools/adapters"
)

const (
	ocrDPI    = 200
	outputDPI = 150
)

type ocrPageResult struct {
	boxes    []gosseract.BoundingBox
	jpegData []byte
	w, h     int
	pageW    float64
	pageH    float64
}

func RunStandalone(inputPath, outputPath string, languages []string, dataDir string, ocrWorkers int) error {
	suppressLeptonicaStderr()

	mtype, err := mimetype.DetectFile(inputPath)
	if err != nil {
		return fmt.Errorf("detect file type: %w", err)
	}

	if strings.HasPrefix(mtype.String(), "image/") {
		return runStandaloneImage(inputPath, outputPath, languages)
	}

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

	fmt.Fprintf(os.Stdout, "starting OCR on %d pages\n", numPages)

	if numPages <= 1 {
		client := newOCRClient(languages)
		defer client.Close()

		for i := range numPages {
			ocrW, ocrH, ocrSamples, ocrPixmap, err := doc.RenderPage(mupdfCtx, i, ocrDPI)
			if err != nil {
				return fmt.Errorf("page %d: render error: %w", i, err)
			}
			res, err := computePage(ocrSamples, ocrW, ocrH, client)
			adapters.FreePixmap(mupdfCtx, ocrPixmap)
			if err != nil {
				return fmt.Errorf("page %d: %w", i, err)
			}
			addPDFPage(pdf, res, i)
		}
	} else {
		type pageJob struct {
			index      int
			ocrSamples []byte
			ocrW, ocrH int
		}

		type pageResult struct {
			index int
			res   ocrPageResult
			err   error
		}

		numWorkers := ocrWorkers
		if numWorkers <= 0 {
			numWorkers = runtime.NumCPU()
		}
		numWorkers = min(numWorkers, numPages, runtime.NumCPU()*2)

		jobs := make(chan pageJob, numWorkers)
		results := make(chan pageResult, numPages)
		var wg sync.WaitGroup

		for range numWorkers {
			wg.Go(func() {
				worker := newOCRClient(languages)
				defer worker.Close()

				for job := range jobs {
					res, err := computePage(job.ocrSamples, job.ocrW, job.ocrH, worker)
					if err != nil {
						results <- pageResult{index: job.index, err: fmt.Errorf("page %d: %w", job.index, err)}
						continue
					}
					results <- pageResult{index: job.index, res: res}
				}
			})
		}

		var phase1Err error
		for i := range numPages {
			if i > 0 && i%50 == 0 {
				fmt.Fprintf(os.Stdout, "OCR page %d/%d\n", i+1, numPages)
			}
			ocrW, ocrH, ocrSamples, ocrPixmap, err := doc.RenderPage(mupdfCtx, i, ocrDPI)
			if err != nil {
				phase1Err = fmt.Errorf("page %d: render error: %w", i, err)
				break
			}
			jobs <- pageJob{
				index:      i,
				ocrSamples: ocrSamples,
				ocrW:       ocrW,
				ocrH:       ocrH,
			}
			adapters.FreePixmap(mupdfCtx, ocrPixmap)
		}
		close(jobs)

		go func() {
			wg.Wait()
			close(results)
		}()

		resultsByIndex := make([]pageResult, numPages)
		for r := range results {
			resultsByIndex[r.index] = r
		}

		if phase1Err != nil {
			return phase1Err
		}

		for _, r := range resultsByIndex {
			if r.err != nil {
				return r.err
			}
		}

		for i := range numPages {
			addPDFPage(pdf, resultsByIndex[i].res, i)
		}
	}

	if err := pdf.OutputFileAndClose(outputPath); err != nil {
		return fmt.Errorf("save output PDF: %w", err)
	}
	return nil
}

func runStandaloneImage(inputPath, outputPath string, languages []string) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open image: %w", err)
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	if w == 0 || h == 0 {
		return fmt.Errorf("image has zero dimensions")
	}

	const maxPixels = 50_000_000
	if int64(w)*int64(h) > maxPixels {
		return fmt.Errorf("image dimensions %dx%d (%d pixels) exceed maximum %d", w, h, w*h, maxPixels)
	}

	samples := imageToRGB(img)

	client := newOCRClient(languages)
	defer client.Close()

	res, err := computePage(samples, w, h, client)
	if err != nil {
		return fmt.Errorf("image OCR: %w", err)
	}

	pdf := fpdf.New("P", "pt", "", "")
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)
	addPDFPage(pdf, res, 0)

	if err := pdf.OutputFileAndClose(outputPath); err != nil {
		return fmt.Errorf("save output PDF: %w", err)
	}
	return nil
}

func newOCRClient(languages []string) *gosseract.Client {
	client := gosseract.NewClient()
	client.SetLanguage(LangString(languages))
	client.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK)
	client.DisableOutput()
	client.SetVariable("load_system_dawg", "false")
	client.SetVariable("load_freq_dawg", "false")
	client.SetVariable("tessedit_ocr_engine_mode", "1")
	return client
}

func computePage(samples []byte, w, h int, client *gosseract.Client) (ocrPageResult, error) {
	outW := w * outputDPI / ocrDPI
	outH := h * outputDPI / ocrDPI

	lowSamples := downscaleRGB(samples, w, h, outW, outH)
	lowImg := samplesToRGBA(lowSamples, outW, outH)
	ocrImg := samplesToRGBA(samples, w, h)

	pngData, err := encodePNG(ocrImg)
	ocrImg = nil
	if err != nil {
		return ocrPageResult{}, fmt.Errorf("PNG encode error: %w", err)
	}

	client.SetImageFromBytes(pngData)
	boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)
	pngData = nil
	if err != nil {
		return ocrPageResult{}, fmt.Errorf("OCR error: %w", err)
	}

	jpegData, err := encodeJPEG(lowImg, 60)
	lowImg = nil
	if err != nil {
		return ocrPageResult{}, fmt.Errorf("JPEG encode error: %w", err)
	}

	pageW := float64(outW) * 72.0 / outputDPI
	pageH := float64(outH) * 72.0 / outputDPI

	return ocrPageResult{
		boxes:    boxes,
		jpegData: jpegData,
		w:        w,
		h:        h,
		pageW:    pageW,
		pageH:    pageH,
	}, nil
}

func addPDFPage(pdf *fpdf.Fpdf, res ocrPageResult, idx int) {
	imgName := fmt.Sprintf("kushim-page-%d", idx)
	pdf.AddPageFormat("P", fpdf.SizeType{Wd: res.pageW, Ht: res.pageH})
	pdf.RegisterImageReader(imgName, "jpg", bytes.NewReader(res.jpegData))
	pdf.Image(imgName, 0, 0, res.pageW, res.pageH, false, "", 0, "")

	scaleX := res.pageW / float64(res.w)
	scaleY := res.pageH / float64(res.h)

	pdf.SetTextRenderingMode(3)
	for _, box := range res.boxes {
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

func downscaleRGB(in []byte, inW, inH int, outW, outH int) []byte {
	out := make([]byte, outW*outH*3)
	for y := range outH {
		srcY := y * inH / outH
		for x := range outW {
			srcX := x * inW / outW
			si := (srcY*inW + srcX) * 3
			di := (y*outW + x) * 3
			out[di] = in[si]
			out[di+1] = in[si+1]
			out[di+2] = in[si+2]
		}
	}
	return out
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
