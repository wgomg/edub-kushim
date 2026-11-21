//go:build cgo

package ocr

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/gabriel-vasile/mimetype"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	gosseract "github.com/otiai10/gosseract/v2"
	_mime "github.com/wgomg/edub-kushim/internal/documents/mime"
	_ "golang.org/x/image/tiff"

	"github.com/wgomg/edub-kushim/internal/cgo"
)

const (
	ocrDPI    = 200
	outputDPI = 150

	tileMaxPixels = 4_000_000
	tileOverlapPx = 128

	// tileMaxPerPage rejects degenerate strip pages whose tile count would dwarf any useful OCR output;
	// a square page at the 100 MP render cap needs 25 tiles
	tileMaxPerPage = 256

	// tileMaxPerDoc bounds the per-document tile count; plan and result preallocation then never exceeds
	// a proportional size, and even a 4,000-page A4 book is only 4,000 tiles
	tileMaxPerDoc = 100_000

	// chunkMaxPages caps how many pages one chunk's fpdf instance retains; every chunk re-embeds the
	// font in the pre-optimizer output, so this bounds that duplication too
	chunkMaxPages = 64

	// chunkMaxPixels caps the summed page pixels in one chunk; the probe measures pages exactly and
	// retained output bytes scale with page pixels, so a page count alone would not bound monster pages
	chunkMaxPixels = 256_000_000
)

type ocrTileResult struct {
	jpegData []byte
	core     image.Rectangle
}

type ocrPageResult struct {
	tiles []ocrTileResult
	boxes []gosseract.BoundingBox
	w, h  int
}

type tileSpec struct {
	core   image.Rectangle
	render image.Rectangle
}

// RunStandalone OCRs one file: images directly, everything else rendered page by page through MuPDF,
// recognized by a worker pool of Tesseract engines, and written back as a searchable PDF that carries
// an invisible-text overlay at reduced DPI. Pages larger than the tile budget are rendered, recognized
// and placed as a grid of bounded tiles, so the per-page working set does not grow with the page's declared
// dimensions. Multi-page output is assembled into bounded chunks, joining them with MuPDF only when
// more than one chunk is produced, so the PDF builder's retention does not grow with the document either.
// It is the body of the internal-ocr verb.
func RunStandalone(inputPath, outputPath string, languages []string, dataDir string, ocrWorkers int) error {
	suppressLeptonicaStderr()

	mtype, err := mimetype.DetectFile(inputPath)
	if err != nil {
		return fmt.Errorf("detect file type: %w", err)
	}

	if _mime.IsImage(mtype.String()) {
		return runStandaloneImage(inputPath, outputPath, languages)
	}

	mupdfCtx, err := cgo.NewMuContext()
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

	_, _ = fmt.Fprintf(os.Stdout, "starting OCR on %d pages\n", numPages)

	pageW := make([]int, numPages)
	pageH := make([]int, numPages)
	plans := make([][]tileSpec, numPages)
	totalTiles := 0
	for i := range numPages {
		w, h, err := doc.PageSizePixels(mupdfCtx, i, ocrDPI)
		if err != nil {
			return fmt.Errorf("page %d: measure error: %w", i, err)
		}
		pageW[i], pageH[i] = w, h
		specs, err := planTiles(w, h)
		if err != nil {
			return fmt.Errorf("page %d: %w", i, err)
		}
		plans[i] = specs
		totalTiles += len(specs)
		if totalTiles > tileMaxPerDoc {
			return fmt.Errorf("document needs at least %d tiles, over the %d-tile limit",
				totalTiles, tileMaxPerDoc)
		}
	}

	if numPages <= 1 {
		client := newOCRClient(languages)
		defer client.Close()

		pdf := newChunkPDF()
		for i := range numPages {
			res, err := processPage(mupdfCtx, doc, i, plans[i], pageW[i], pageH[i], client)
			if err != nil {
				return fmt.Errorf("page %d: %w", i, err)
			}
			addPDFPage(pdf, res, i)
		}
		if err := pdf.OutputFileAndClose(outputPath); err != nil {
			return fmt.Errorf("save output PDF: %w", err)
		}
		return nil
	}

	chunks := planChunks(pageW, pageH)
	chunkTiles := make([]int, len(chunks))
	maxChunkTiles := 0
	for c, ch := range chunks {
		for i := ch.firstPage; i < ch.lastPage; i++ {
			chunkTiles[c] += len(plans[i])
		}
		maxChunkTiles = max(maxChunkTiles, chunkTiles[c])
	}

	docUUID := uuid.New().String()
	var chunkPaths []string
	defer func() {
		for _, p := range chunkPaths {
			_ = os.Remove(p)
		}
	}()

	type tileJob struct {
		pageIndex int
		tileIndex int
		core      image.Rectangle
		samples   []byte
		tw, th    int
		ox, oy    int
	}

	type tileResult struct {
		pageIndex int
		tileIndex int
		boxes     []gosseract.BoundingBox
		jpegData  []byte
		err       error
	}

	numWorkers := ocrWorkers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	numWorkers = min(numWorkers, totalTiles, runtime.NumCPU()*2)

	jobs := make(chan tileJob, numWorkers)
	// cap results at the largest chunk's tile count: workers must never block sending a result while the
	// producer is still pushing this chunk's jobs, or the full jobs and results channels deadlock
	results := make(chan tileResult, maxChunkTiles)
	var wg sync.WaitGroup

	for range numWorkers {
		wg.Go(func() {
			worker := newOCRClient(languages)
			defer worker.Close()

			for job := range jobs {
				boxes, jpegData, err := computeTile(job.samples, job.tw, job.th, job.core, job.ox, job.oy, worker)
				if err != nil {
					results <- tileResult{
						pageIndex: job.pageIndex,
						tileIndex: job.tileIndex,
						err:       fmt.Errorf("page %d: tile %d: %w", job.pageIndex, job.tileIndex, err),
					}
					continue
				}
				results <- tileResult{
					pageIndex: job.pageIndex,
					tileIndex: job.tileIndex,
					boxes:     boxes,
					jpegData:  jpegData,
				}
			}
		})
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var runErr error
chunkLoop:
	for c, ch := range chunks {
		slots := make([][]tileResult, ch.lastPage-ch.firstPage)
		for i := ch.firstPage; i < ch.lastPage; i++ {
			slots[i-ch.firstPage] = make([]tileResult, len(plans[i]))
		}

	renderLoop:
		for i := ch.firstPage; i < ch.lastPage; i++ {
			if i > 0 && i%10 == 0 {
				_, _ = fmt.Fprintf(os.Stdout, "OCR page %d/%d\n", i+1, numPages)
			}
			for t, spec := range plans[i] {
				r := spec.render
				samples, tw, th, ox, oy, err := doc.RenderTile(mupdfCtx, i, ocrDPI,
					r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
				if err != nil {
					runErr = fmt.Errorf("page %d: tile %d: render error: %w", i, t, err)
					break renderLoop
				}
				jobs <- tileJob{
					pageIndex: i,
					tileIndex: t,
					core:      spec.core,
					samples:   samples,
					tw:        tw,
					th:        th,
					ox:        ox,
					oy:        oy,
				}
			}
		}
		if runErr != nil {
			break chunkLoop
		}

		for range chunkTiles[c] {
			r := <-results
			slots[r.pageIndex-ch.firstPage][r.tileIndex] = r
		}

		pdf := newChunkPDF()
		for i := ch.firstPage; i < ch.lastPage; i++ {
			res := ocrPageResult{w: pageW[i], h: pageH[i], tiles: make([]ocrTileResult, 0, len(plans[i]))}
			for t := range slots[i-ch.firstPage] {
				r := slots[i-ch.firstPage][t]
				if r.err != nil {
					runErr = r.err
					break
				}
				res.tiles = append(res.tiles, ocrTileResult{jpegData: r.jpegData, core: plans[i][t].core})
				res.boxes = append(res.boxes, r.boxes...)
			}
			if runErr != nil {
				break
			}
			addPDFPage(pdf, res, i)
			// fpdf now owns the encoded images; drop this page's chunk results so the slot and builder
			// retention do not stack
			slots[i-ch.firstPage] = nil
		}
		if runErr != nil {
			break chunkLoop
		}

		chunkPath := filepath.Join(os.TempDir(), fmt.Sprintf("ocr_chunk_%s_%03d.pdf", docUUID, c))
		chunkPaths = append(chunkPaths, chunkPath)
		if err := pdf.OutputFileAndClose(chunkPath); err != nil {
			runErr = fmt.Errorf("write chunk %d: %w", c, err)
			break chunkLoop
		}
	}

	close(jobs)
	// workers must never stay blocked sending their last in-flight results, or the closer goroutine
	// never runs and this drain never terminates
	for range results {
	}

	if runErr != nil {
		return runErr
	}

	if len(chunkPaths) == 1 {
		// rename is the fast path, but it fails across filesystems (temp vs output directories); the
		// join below then produces the same file
		if err := os.Rename(chunkPaths[0], outputPath); err == nil {
			return nil
		}
	}

	if err := mupdfCtx.JoinPDFs(outputPath, chunkPaths); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("join chunks: %w", err)
	}
	return nil
}

// processPage renders, OCRs and assembles all of one page's tiles in order, acting as the single-page
// counterpart of the producer/worker split used for multi-page documents.
func processPage(ctx *cgo.MuContext, doc *cgo.MuDocument, pageNum int, specs []tileSpec, w, h int, client *gosseract.Client) (ocrPageResult, error) {
	res := ocrPageResult{w: w, h: h, tiles: make([]ocrTileResult, 0, len(specs))}
	for _, spec := range specs {
		r := spec.render
		samples, tw, th, ox, oy, err := doc.RenderTile(ctx, pageNum, ocrDPI,
			r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
		if err != nil {
			return ocrPageResult{}, fmt.Errorf("tile render error: %w", err)
		}
		boxes, jpegData, err := computeTile(samples, tw, th, spec.core, ox, oy, client)
		if err != nil {
			return ocrPageResult{}, err
		}
		res.tiles = append(res.tiles, ocrTileResult{jpegData: jpegData, core: spec.core})
		res.boxes = append(res.boxes, boxes...)
	}
	return res, nil
}

func runStandaloneImage(inputPath, outputPath string, languages []string) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open image: %w", err)
	}
	img, _, err := image.Decode(f)
	_ = f.Close()
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

	core := image.Rect(0, 0, w, h)
	boxes, jpegData, err := computeTile(samples, w, h, core, 0, 0, client)
	if err != nil {
		return fmt.Errorf("image OCR: %w", err)
	}

	res := ocrPageResult{
		tiles: []ocrTileResult{{jpegData: jpegData, core: core}},
		boxes: boxes,
		w:     w,
		h:     h,
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

// planTiles splits a page of w x h pixels into row-major tiles whose cores partition the page exactly;
// cores stay within the tile budget. Multi-part axes keep every core at least 4 pixels wide so the 150
// DPI downscale never collapses. The degenerate single-part case (an axis under 4 pixels) is rejected
// by computeTile's empty-crop guard. Each render rectangle is its core expanded by tileOverlapPx on
// every side (overlap included for OCR only) and clamped to the page. A page within the tile budget
// yields a single tile whose core is the whole page. Pages that would need more than tileMaxPerPage
// tiles are rejected as degenerate strip input.
func planTiles(w, h int) ([]tileSpec, error) {
	page := image.Rect(0, 0, w, h)
	if w*h <= tileMaxPixels {
		return []tileSpec{{core: page, render: page}}, nil
	}

	xs := coreBounds(w)
	ys := coreBounds(h)
	cols, rows := len(xs)-1, len(ys)-1
	if cols*rows > tileMaxPerPage {
		return nil, fmt.Errorf("%dx%d px page needs %d tiles, over the %d-tile limit",
			w, h, cols*rows, tileMaxPerPage)
	}

	specs := make([]tileSpec, 0, cols*rows)
	for r := range rows {
		for c := range cols {
			core := image.Rect(xs[c], ys[r], xs[c+1], ys[r+1])
			render := image.Rect(core.Min.X-tileOverlapPx, core.Min.Y-tileOverlapPx,
				core.Max.X+tileOverlapPx, core.Max.Y+tileOverlapPx).Intersect(page)
			specs = append(specs, tileSpec{core: core, render: render})
		}
	}
	return specs, nil
}

// coreBounds splits one page axis of total pixels into ascending core boundaries ending at the total.
// Parts are an even split of the axis, rounded up to a multiple of 4 and capped at maxDim; the last
// span takes the remainder, borrowing 4 pixels from its neighbour when the raw remainder is under 4
// so every core survives the 150 DPI downscale.
func coreBounds(total int) []int {
	maxDim := max(int(math.Sqrt(float64(tileMaxPixels)))&^3, 4)
	parts := (total + maxDim - 1) / maxDim
	base := ((total + parts - 1) / parts) + 3
	base = min(base&^3, maxDim)

	bounds := make([]int, parts+1)
	for i := 1; i < parts; i++ {
		bounds[i] = i * base
	}
	bounds[parts] = total
	if parts > 1 && total-bounds[parts-1] < 4 {
		bounds[parts-1] -= 4
	}
	return bounds
}

// chunkRange is a half-open page interval [firstPage, lastPage) assembled into one chunk PDF.
type chunkRange struct {
	firstPage int
	lastPage  int
}

// planChunks groups pages into chunks whose retained PDF state stays bounded: a chunk closes before
// the page that would push it past chunkMaxPages pages or chunkMaxPixels of summed page pixels. Every
// chunk holds at least one page, so a lone oversized page only ever exceeds the pixel budget on its
// own. Chunks cover every page in order and partition the document exactly.
func planChunks(pageW, pageH []int) []chunkRange {
	chunks := make([]chunkRange, 0, len(pageW)/chunkMaxPages+1)
	first, pixels := 0, 0
	for i := range pageW {
		pagePixels := pageW[i] * pageH[i]
		if i > first && (i-first >= chunkMaxPages || pixels+pagePixels > chunkMaxPixels) {
			chunks = append(chunks, chunkRange{firstPage: first, lastPage: i})
			first, pixels = i, 0
		}
		pixels += pagePixels
	}
	if first < len(pageW) {
		chunks = append(chunks, chunkRange{firstPage: first, lastPage: len(pageW)})
	}
	return chunks
}

// computeTile OCRs one rendered tile and returns the boxes it owns together with the JPEG for its core
// region. core is the tile's core rectangle in page pixels; ox, oy are the pixmap origin reported by
// RenderTile. A box belongs to the tile whose core contains its centre, so words in the overlap are
// counted once; kept boxes are returned in page-pixel coordinates. The JPEG covers the core only, so
// placed images partition the page without double-painted overlap.
func computeTile(samples []byte, tw, th int, core image.Rectangle, ox, oy int, client *gosseract.Client) ([]gosseract.BoundingBox, []byte, error) {
	ocrImg := samplesToRGBA(samples, tw, th)

	pngData, err := encodePNG(ocrImg)
	ocrImg = nil
	if err != nil {
		return nil, nil, fmt.Errorf("PNG encode error: %w", err)
	}

	client.SetImageFromBytes(pngData)
	boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)
	pngData = nil
	if err != nil {
		return nil, nil, fmt.Errorf("OCR error: %w", err)
	}

	coreLocal := core.Sub(image.Pt(ox, oy))
	kept := boxes[:0]
	for _, box := range boxes {
		centre := image.Pt((box.Box.Min.X+box.Box.Max.X)/2, (box.Box.Min.Y+box.Box.Max.Y)/2)
		if !centre.In(coreLocal) {
			continue
		}
		box.Box = box.Box.Add(image.Pt(ox, oy))
		kept = append(kept, box)
	}

	outW := tw * outputDPI / ocrDPI
	outH := th * outputDPI / ocrDPI

	lowSamples := downscaleRGB(samples, tw, th, outW, outH)
	lowImg := samplesToRGBA(lowSamples, outW, outH)

	coreLow := image.Rect(
		coreLocal.Min.X*outputDPI/ocrDPI,
		coreLocal.Min.Y*outputDPI/ocrDPI,
		coreLocal.Max.X*outputDPI/ocrDPI,
		coreLocal.Max.Y*outputDPI/ocrDPI,
	).Intersect(image.Rect(0, 0, outW, outH))

	// pdf pages always render at least 3 pixels per axis, so an empty crop can only originate from a tiny
	// decoded image; fail loudly instead of encoding an invalid zero-dimension JPEG
	if coreLow.Empty() {
		return nil, nil, fmt.Errorf("tile core %v maps to an empty crop at %d DPI (%dx%d px tile)",
			coreLocal, outputDPI, tw, th)
	}

	jpegData, err := encodeJPEG(lowImg.SubImage(coreLow), 60)
	lowImg = nil
	if err != nil {
		return nil, nil, fmt.Errorf("JPEG encode error: %w", err)
	}

	return kept, jpegData, nil
}

type fontMetrics struct {
	ascent  float64
	descent float64
}

// newChunkPDF returns an fpdf instance with the shared font and page settings already applied. Each
// chunk, and the single-page path, assembles into its own instance: OutputFileAndClose finalizes and
// closes the instance, so a fresh one per chunk is what bounds the builder's retention.
func newChunkPDF() *fpdf.Fpdf {
	pdf := fpdf.New("P", "pt", "", "")
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)
	return pdf
}

func addPDFPage(pdf *fpdf.Fpdf, res ocrPageResult, idx int) {
	scale := 72.0 / ocrDPI
	pageW := float64(res.w) * scale
	pageH := float64(res.h) * scale

	pdf.AddPageFormat("P", fpdf.SizeType{Wd: pageW, Ht: pageH})

	for t, tile := range res.tiles {
		imgName := fmt.Sprintf("kushim-page-%d-%d", idx, t)
		pdf.RegisterImageReader(imgName, "jpg", bytes.NewReader(tile.jpegData))
		pdf.Image(imgName,
			float64(tile.core.Min.X)*scale,
			float64(tile.core.Min.Y)*scale,
			float64(tile.core.Dx())*scale,
			float64(tile.core.Dy())*scale,
			false, "", 0, "")
	}

	desc := pdf.GetFontDesc("KushimText", "")
	metrics := fontMetrics{ascent: float64(desc.Ascent), descent: float64(desc.Descent)}

	pdf.SetTextRenderingMode(3)
	for _, box := range res.boxes {
		width := float64(box.Box.Dx()) * scale
		height := float64(box.Box.Dy()) * scale
		if width < 1 || height < 1 {
			continue
		}
		left := float64(box.Box.Min.X) * scale
		fontSize, baseline := wordPlacement(height, float64(box.Box.Max.Y)*scale, metrics)
		pdf.SetFont("KushimText", "", fontSize)
		if natural := pdf.GetStringWidth(box.Word); natural > 0 {
			if scalePct := stretchScale(width, natural); scalePct > 0 {
				pdf.TransformBegin()
				pdf.TransformScaleX(scalePct, left, baseline)
				pdf.Text(left, baseline, box.Word)
				pdf.TransformEnd()
				continue
			}
		}
		pdf.Text(left, baseline, box.Word)
	}
}

func wordPlacement(height, bottom float64, m fontMetrics) (fontSize, baseline float64) {
	if m.ascent-m.descent <= 0 {
		fontSize = height * 0.8
		if fontSize < 2 {
			fontSize = 2
		}
		return fontSize, bottom + height*0.2
	}
	fontSize = height * 1000 / (m.ascent - m.descent)
	if fontSize < 2 {
		fontSize = 2
	}
	return fontSize, bottom - m.descent/1000*fontSize
}

func stretchScale(boxWidth, naturalWidth float64) float64 {
	scalePct := 100 * boxWidth / naturalWidth
	if scalePct < 25 || scalePct > 400 {
		return 0
	}
	return scalePct
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
