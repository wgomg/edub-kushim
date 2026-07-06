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
	"sync"

	"github.com/go-pdf/fpdf"
	gosseract "github.com/otiai10/gosseract/v2"

	"github.com/wgomg/edub-kushim/internal/tools/adapters"
)

func RunStandalone(inputPath, outputPath string, languages []string, dataDir string, ocrWorkers int) error {
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

	const ocrDPI = 200
	const outputDPI = 150

	if numPages <= 1 {
		client := gosseract.NewClient()
		defer client.Close()
		client.SetLanguage(LangString(languages))
		client.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK)
		client.DisableOutput()
		client.SetVariable("load_system_dawg", "false")
		client.SetVariable("load_freq_dawg", "false")
		client.SetVariable("tessedit_ocr_engine_mode", "1")

		for i := range numPages {
			ocrW, ocrH, ocrSamples, ocrPixmap, err := doc.RenderPage(mupdfCtx, i, ocrDPI)
			if err != nil {
				return fmt.Errorf("page %d: render error: %w", i, err)
			}

			lowW := ocrW * outputDPI / ocrDPI
			lowH := ocrH * outputDPI / ocrDPI

			lowSamples := downscaleRGB(ocrSamples, ocrW, ocrH, lowW, lowH)
			lowImg := samplesToRGBA(lowSamples, lowW, lowH)

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
	} else {
		type pageJob struct {
			index        int
			ocrSamples   []byte
			ocrW, ocrH   int
			outW, outH   int
			pageW, pageH float64
		}

		type pageResult struct {
			index        int
			boxes        []gosseract.BoundingBox
			jpegData     []byte
			ocrW, ocrH   int
			pageW, pageH float64
			err          error
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
				worker := gosseract.NewClient()
				defer worker.Close()
				worker.SetLanguage(LangString(languages))
				worker.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK)
				worker.DisableOutput()
				worker.SetVariable("load_system_dawg", "false")
				worker.SetVariable("load_freq_dawg", "false")
				worker.SetVariable("tessedit_ocr_engine_mode", "1")

				for job := range jobs {
					lowSamples := downscaleRGB(job.ocrSamples, job.ocrW, job.ocrH, job.outW, job.outH)
					lowImg := samplesToRGBA(lowSamples, job.outW, job.outH)

					ocrImg := samplesToRGBA(job.ocrSamples, job.ocrW, job.ocrH)

					pngData, err := encodePNG(ocrImg)
					ocrImg = nil
					if err != nil {
						results <- pageResult{index: job.index, err: fmt.Errorf("page %d: PNG encode error: %w", job.index, err)}
						continue
					}

					worker.SetImageFromBytes(pngData)
					boxes, err := worker.GetBoundingBoxes(gosseract.RIL_WORD)
					pngData = nil
					if err != nil {
						results <- pageResult{index: job.index, err: fmt.Errorf("page %d: OCR error: %w", job.index, err)}
						continue
					}

					jpegData, err := encodeJPEG(lowImg, 60)
					lowImg = nil
					if err != nil {
						results <- pageResult{index: job.index, err: fmt.Errorf("page %d: JPEG encode error: %w", job.index, err)}
						continue
					}

					results <- pageResult{
						index:    job.index,
						boxes:    boxes,
						jpegData: jpegData,
						ocrW:     job.ocrW,
						ocrH:     job.ocrH,
						pageW:    job.pageW,
						pageH:    job.pageH,
					}
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
			lowW := ocrW * outputDPI / ocrDPI
			lowH := ocrH * outputDPI / ocrDPI

			jobs <- pageJob{
				index:      i,
				ocrSamples: ocrSamples,
				ocrW:       ocrW,
				ocrH:       ocrH,
				outW:       lowW,
				outH:       lowH,
				pageW:      float64(lowW) * 72.0 / outputDPI,
				pageH:      float64(lowH) * 72.0 / outputDPI,
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
			r := resultsByIndex[i]
			pdf.AddPageFormat("P", fpdf.SizeType{Wd: r.pageW, Ht: r.pageH})

			imgName := fmt.Sprintf("kushim-page-%d", i)
			pdf.RegisterImageReader(imgName, "jpg", bytes.NewReader(r.jpegData))
			pdf.Image(imgName, 0, 0, r.pageW, r.pageH, false, "", 0, "")

			scaleX := r.pageW / float64(r.ocrW)
			scaleY := r.pageH / float64(r.ocrH)

			pdf.SetTextRenderingMode(3)
			for _, box := range r.boxes {
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
	}

	if err := pdf.OutputFileAndClose(outputPath); err != nil {
		return fmt.Errorf("save output PDF: %w", err)
	}
	return nil
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
