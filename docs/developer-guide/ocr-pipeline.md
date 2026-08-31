# Developer Guide — The OCR Pipeline and Searchable PDFs

This guide explains how edub-kushim turns scanned PDFs and images into
searchable documents: the two OCR engines, the self-reexec subprocess
architecture, page rendering, the parallel worker pipeline, searchable-PDF
assembly with an invisible text layer, tessdata management, and the
post-OCR optimization step.

Audience: developers working on `internal/tools/adapters/ocr/`, or anyone
tuning OCR quality/performance. Companion: `cgo.md` (the
MuPDF/Tesseract wrapper layer underneath).

---

## Table of contents

1. [Orientation](#1-orientation)
2. [Two engines, one interface](#2-two-engines-one-interface)
3. [The self-reexec architecture](#3-the-self-reexec-architecture)
4. [Rendering pages with MuPDF](#4-rendering-pages-with-mupdf)
5. [The parallel page pipeline](#5-the-parallel-page-pipeline)
6. [Per-page OCR: images and boxes](#6-per-page-ocr-images-and-boxes)
7. [Searchable PDF assembly](#7-searchable-pdf-assembly)
8. [The image path](#8-the-image-path)
9. [Tessdata management](#9-tessdata-management)
10. [Error tolerance](#10-error-tolerance)
11. [The ocrmypdf engine](#11-the-ocrmypdf-engine)
12. [Post-OCR optimization](#12-post-ocr-optimization)
13. [Configuration knobs](#13-configuration-knobs)
14. [Gotchas](#14-gotchas)

---

## 1. Orientation

The OCR step sits between text extraction and storage in the consume
pipeline: if a document has no extractable text layer (a scan), the consumer
calls the OCR tool (`tools.Runner.OCR`), which produces a **searchable PDF** —
the original pages rendered as images *plus* an invisible, selectable text
layer. That PDF becomes the stored document, and the extracted text feeds
full-text search.

Everything lives in `internal/tools/adapters/ocr/`:

| File | Role |
|---|---|
| `adapter.go` | the `OCR` interface + engine factory |
| `gosseract.go` | the built-in engine (cgo): spawns the subprocess, optimizes output |
| `standalone.go` | `internal-ocr`: rendering → OCR → searchable PDF (cgo) |
| `ocrmypdf.go` | the external-tool engine |
| `tessdata.go` | language data download/management |
| `font_embed.go` | the embedded Unicode TrueType font for text layers |

Two engines are selectable via `consumer.ocr.engine`
(`internal/config/config.go`): **`gosseract`** (built-in, cgo — the default)
and **`ocrmypdf`** (external Python tool).

---

## 2. Two engines, one interface

```go
// internal/tools/adapters/ocr/adapter.go:11-15
type OCR interface {
	Process(ctx context.Context, docId, path string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}
```

`Process` returns the path of the produced searchable PDF (`*string`; nil on
failure). The factory (`adapter.go:24-33`) switches on the configured engine,
with `newGosseract` as the cgo-gated hook (§8 of the cgo guide):

```go
func NewOCR(logger *utils.Logger, cfg config.ToolConfig, pdfOptimizerCmd string, languages []string, dataDir string, ocrWorkers int) (OCR, error) {
	switch cfg.Command {
	case config.OCR.OcrMyPdf:
		return NewOcrMyPdf(logger, cfg, languages)
	case config.OCR.Gosseract:
		return newGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir, ocrWorkers)
	default:
		return newGosseract(logger, cfg, pdfOptimizerCmd, languages, dataDir, ocrWorkers)
	}
}
```

Both engines share the same contract and the same "output a searchable PDF"
semantics, but the built-in one is fully self-contained (no system packages
beyond what `make build-deps` produces), while ocrmypdf needs Python + its
own dependencies (unpaper, pngquant, ...).

---

## 3. The self-reexec architecture

The gosseract engine never runs Tesseract *in the parent process*. Instead it
re-executes the current binary with a hidden subcommand
(`internal/tools/adapters/ocr/gosseract.go:56-65`):

```go
outPath := filepath.Join(os.TempDir(), "ocr_"+uuid.New().String()+".pdf")

langStr := LangString(o.languages)
cmd := exec.CommandContext(ctx, os.Args[0], "internal-ocr",
	"--input", path,
	"--output", outPath,
	"--languages", langStr,
	"--datadir", o.dataDir,
	"--ocr-workers", strconv.Itoa(o.ocrWorkers),
)
```

`os.Args[0]` is the running `kushim` binary; `cmd/kushim/main.go`
special-cases `internal-ocr` before normal command dispatch. Why this design:

- **Crash isolation** — Tesseract/Leptonica are C code; a segfault in the
  child kills only the child. The parent sees an exit error, cleans up, and
  the task fails gracefully instead of taking down the worker process.
- **Memory isolation** — the 2–5 GB class of RSS this work can reach is
  charged to the child, not the long-running daemon.
- **Simple parallelism** — the child owns all worker goroutines for
  multi-page files.

The parent streams the child's stdout line-by-line into its own logger
(`gosseract.go:80-85`):

```go
go func() {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		o.logger.Info(&docId, "%s", scanner.Text())
	}
}()
```

and the child prints progress lines (`"starting OCR on %d pages"`,
`"OCR page 50/200"`) that become log output. The context passed to
`exec.CommandContext` cancels the child on timeout or shutdown.

---

## 4. Rendering pages with MuPDF

Inside the child (`standalone.go:37-176`), each page is rendered from the
PDF at **200 DPI** via the cgo wrapper:

```go
const (
	ocrDPI    = 200
	outputDPI = 150
)
...
ocrW, ocrH, ocrSamples, ocrPixmap, err := doc.RenderPage(mupdfCtx, i, ocrDPI)
...
adapters.FreePixmap(mupdfCtx, ocrPixmap)   // samples are copied; pixmap must be freed
```

- `ocrDPI` (200) is the OCR resolution — Tesseract's quality sweet spot.
- `outputDPI` (150) is the resolution of the *image embedded in the output
  PDF* — smaller files, still readable.
- The render is bounded by `MUPDF_MAX_RENDER_PIXELS` (100 MP) in the C
  wrapper (`mupdf_wrapper.go:107`) — a malicious page size can't exhaust
  memory (see `cgo.md` §9).
- The pixmap is freed **immediately after** the samples are copied — the
  samples are Go-owned, the pixmap C-owned (`standalone.go:82`).

Single-page files take a simple loop; multi-page files take the parallel
pipeline (§5).

---

## 5. The parallel page pipeline

For `numPages > 1`, pages are OCR'd by a worker pool
(`standalone.go:88-169`):

```go
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
		worker := newOCRClient(languages)     // one Tesseract client per worker
		defer worker.Close()
		for job := range jobs {
			res, err := computePage(job.ocrSamples, job.ocrW, job.ocrH, worker)
			...
			results <- pageResult{index: job.index, res: res}
		}
	})
}
```

- **One Tesseract client per worker** — gosseract clients are not
  concurrency-safe, so each worker owns its own.
- The producer renders pages and feeds `jobs` (buffered to `numWorkers`),
  freeing each pixmap right after enqueue (`standalone.go:132-144`); the
  producer closes `jobs`, a closer goroutine closes `results` after
  `wg.Wait()`.
- **Deterministic output**: results are collected by index
  (`resultsByIndex[r.index] = r`, `standalone.go:152-155`) and pages are
  assembled in order — concurrent OCR, sequential PDF.
- Phase-1 render errors abort before assembly (`phase1Err`), and any per-page
  OCR error fails the whole run (`standalone.go:157-165`) — an OCR failure
  on page 12 of a book is a failed task, not a partial PDF.

---

## 6. Per-page OCR: images and boxes

`computePage` (`standalone.go:234-272`) produces everything one output page
needs:

```go
outW := w * outputDPI / ocrDPI
outH := h * outputDPI / ocrDPI

lowSamples := downscaleRGB(samples, w, h, outW, outH)   // nearest-neighbor
lowImg := samplesToRGBA(lowSamples, outW, outH)          // for the output JPEG
ocrImg := samplesToRGBA(samples, w, h)                   // full-res for OCR

pngData, err := encodePNG(ocrImg)
...
client.SetImageFromBytes(pngData)
boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)   // word-level boxes
...
jpegData, err := encodeJPEG(lowImg, 60)                     // the visible page image
```

- Tesseract gets a **lossless PNG** at 200 DPI (compression artifacts hurt
  recognition); the visible image in the output is a **JPEG at quality 60**,
  150 DPI — OCR input and display image are deliberately different.
- `GetBoundingBoxes(RIL_WORD)` returns each recognized word with its pixel
  rectangle — the data needed to lay the text layer over the image.
- `downscaleRGB` is a hand-rolled nearest-neighbor sampler
  (`standalone.go:301-315`) — fast, allocation-light, good enough for a
  display image.

The Tesseract client is tuned for speed and raw boxes
(`newOCRClient`, `standalone.go:223-232`):

```go
client := gosseract.NewClient()
client.SetLanguage(LangString(languages))
client.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK)
client.DisableOutput()                 // we only want boxes, not a text blob
client.SetVariable("load_system_dawg", "false")
client.SetVariable("load_freq_dawg", "false")
client.SetVariable("tessedit_ocr_engine_mode", "1")
```

`DisableOutput` skips the full-text pass (boxes come free with it); the
`dawg` variables disable dictionary files for speed; engine mode 1 selects
the LSTM engine.

---

## 7. Searchable PDF assembly

`addPDFPage` (`standalone.go:279-317`) builds the output with go-pdf/fpdf:

```go
pdf.AddPageFormat("P", fpdf.SizeType{Wd: res.pageW, Ht: res.pageH})  // points
pdf.RegisterImageReader(imgName, "jpg", bytes.NewReader(res.jpegData))
pdf.Image(imgName, 0, 0, res.pageW, res.pageH, false, "", 0, "")

scaleX := res.pageW / float64(res.w)
scaleY := res.pageH / float64(res.h)

desc := pdf.GetFontDesc("KushimText", "")  // Ascent/Descent in 1/1000 em
metrics := fontMetrics{ascent: float64(desc.Ascent), descent: float64(desc.Descent)}

pdf.SetTextRenderingMode(3)          // invisible text!
for _, box := range res.boxes {
	width := float64(box.Box.Dx()) * scaleX
	height := float64(box.Box.Dy()) * scaleY
	if width < 1 || height < 1 {
		continue
	}
	left := float64(box.Box.Min.X) * scaleX
	fontSize, baseline := wordPlacement(height, float64(box.Box.Max.Y)*scaleY, metrics)
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
```

The pieces:

- **Page size in points** derived from the 150-DPI output image
  (`pageW := float64(outW) * 72.0 / outputDPI`, `standalone.go:261-262`).
- **`pdf.SetTextRenderingMode(3)`** — PDF text rendering mode 3 = "neither
  fill nor stroke": the text is **invisible** but present in the content
  stream, which is what makes the PDF searchable/selectable (this is the
  `3 Tr` mode mentioned in `AGENTS.md`).
- **Metrics-driven placement** (`wordPlacement`): each OCR box's pixel rect is
  scaled from OCR resolution to page points (`scaleX/scaleY`); the font size
  is `height * 1000 / (Ascent - Descent)` so the glyph extent fills the box
  height, and the baseline sits one descender depth below the box bottom
  (`Max.Y + (-Descent/1000) * fontSize`) — `pdf.Text` draws from the
  baseline. If the font reports zero metrics, fall back to `height * 0.8`
  with a `height * 0.2` baseline offset.
- **Horizontal stretch to the box width** (`stretchScale`): the word's natural
  width (`pdf.GetStringWidth`) rarely matches the OCR box, so each word is
  scaled with `TransformScaleX` (a CTM `q`/`Q` block centered on the box's
  left edge/baseline) to fill the box. The factor is clamped to 25%–400%;
  out-of-range words keep their natural width.
- The font is the **embedded `kushim_font.ttf`** (Liberation Sans, SIL OFL
  1.1, `font_embed.go:9-13`), registered from bytes
  (`pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)`,
  `standalone.go:68`) — Unicode coverage for any script the OCR produces,
  no system font dependency.

The whole PDF is written with `pdf.OutputFileAndClose(outputPath)`
(`standalone.go:172`).

---

## 8. The image path

If the input is an image rather than a PDF (`runStandaloneImage`,
`standalone.go:178-221`), the flow is the same minus rendering: decode the
image (`image.Decode` with tiff registered via a blank import,
`standalone.go:19`), enforce a **50 MP pixel cap** (`standalone.go:197-200`)
against decompression bombs, convert to RGB, OCR, and wrap in a one-page
searchable PDF:

```go
const maxPixels = 50_000_000
if int64(w)*int64(h) > maxPixels {
	return fmt.Errorf("image dimensions %dx%d (%d pixels) exceed maximum %d", w, h, w*h, maxPixels)
}
```

`imageToRGB` (`standalone.go:350-365`) handles **premultiplied alpha** — the
standard `RGBA()` round trip `(r + 65535 - a) >> 8` un-premultiplies so white
backgrounds with alpha don't come out dark.

---

## 9. Tessdata management

Tesseract needs language data files. `EnsureLanguages`
(`internal/tools/adapters/ocr/tessdata.go:15-48`) downloads missing
`.traineddata` files from the `tessdata_fast` repo:

```go
var ensureOnce sync.Once

func EnsureLanguages(logger *utils.Logger, dataDir string, languages []string) error {
	var err error
	ensureOnce.Do(func() {
		if err = os.MkdirAll(dataDir, 0755); err != nil { ... }
		for _, lang := range languages {
			dest := filepath.Join(dataDir, lang+".traineddata")
			if _, statErr := os.Stat(dest); statErr == nil {
				continue
			}
			url := fmt.Sprintf("https://github.com/tesseract-ocr/tessdata_fast/raw/main/%s.traineddata", lang)
			...
			if dlErr := downloadTessdata(url, dest); dlErr != nil { ... }
		}
		os.Setenv("TESSDATA_PREFIX", dataDir)
	})
	return err
}
```

- `sync.Once` — the whole check/download runs **once per process**, even if
  called from many workers. A failed download poisons the once (the error is
  returned to every subsequent caller) — retry happens at the next process.
- Downloads are sanity-checked: **files under 500 KB are rejected**
  (`tessdata.go:52-55`) — the GitHub error page is bigger than that, but a
  truncated download shouldn't be trusted either.
- `TESSDATA_PREFIX` is set process-wide, pointing Tesseract at the data dir
  (`tessdata.go:44-45`).
- The enricher also auto-adds detected languages to the OCR config and
  triggers the download (`ensureOCRLanguage`, `internal/enrichment/enricher.go:439-491`)
  — see `llm.md` §10.

---

## 10. Error tolerance

Tesseract emits known-harmless warnings on stderr; the parent tolerates them
only when the child still produced output
(`gosseract.go:87-100`):

```go
if err := cmd.Wait(); err != nil {
	_, isExit := err.(*exec.ExitError)
	_, outExists := os.Stat(outPath)
	if isExit && outExists == nil && isNonFatalStderr(stderr.String()) {
		o.logger.Warn(&docId, "OCR subprocess had non-fatal errors: %v (stderr: %s)", err, stderr.String())
	} else {
		os.Remove(outPath)
		return nil, fmt.Errorf("gosseract OCR: %w (stderr: %s)", err, stderr.String())
	}
}
```

`isNonFatalStderr` (`gosseract.go:122-135`) matches the known noise:

```go
patterns := []string{
	"pixReadMemTiff: function not present",
	"font pixa not made",
	"bmfCreate",
}
```

The tolerance is *conditional on the output file existing* — a failing
subprocess with zero output is always an error. After OCR, the output is
always piped through the PDF optimizer (`gosseract.go:104-111`, §12) and the
temporary file removed.

---

## 11. The ocrmypdf engine

The alternative engine delegates to the external `ocrmypdf` tool
(`ocrmypdf.go:37-91`). Constructor validates the binary exists with
`exec.LookPath` and gives install hints (`ocrmypdf.go:25-35`). The command:

```go
args := []string{
	"--language", strings.Join(o.languages, "+"),
	"--output-type", "pdfa-2",
	"--optimize", "2",
	"--rotate-pages",
	"--deskew",
	"--clean",
	"--pdfa-image-compression", "jpeg",
	"--jpeg-quality", "85",
	"--png-quality", "85",
	"--oversample", "150",
	path,
	outputPath,
}
cmd := exec.CommandContext(ctx, o.config.Command, args...)
```

Notable behaviors:

- **Error hints**: stderr is scanned for known failure modes and the error
  gets a concrete suggestion — missing tesseract language packs, missing
  unpaper, missing pngquant/optipng, missing tesseract itself
  (`ocrmypdf.go:47-66`). This is the "helpful error" pattern: diagnose in
  code, not in a wiki.
- It requires absolute input paths (`ocrmypdf.go:40-43`).
- The `--rotate-pages`/`--deskew`/`--clean` flags are OCR-quality features
  the built-in engine doesn't have — the tradeoff for external dependencies.

---

## 12. Post-OCR optimization

Both paths run the produced PDF through the optimizer (`gosseract.go:104-111`):

```go
optResult, err := o.optimizer.Optimize(ctx, docId, outPath)
```

The optimizer is the `pdfoptimizer` tool: **Ghostscript** (`gs
-dPDFSETTINGS=/ebook ...`) or the **MuPDF clean path** (`mupdf_pdf_clean_file`
with `NewCleanOptions`, `mupdf_wrapper.go:291-340`), which replicates the
ebook settings: garbage collection, dedup, compression, content sanitization,
image downsampling (colour/grayscale → 150 DPI JPEG @85, bitonal → 300 DPI
CCITT Fax). The invisible text layer survives this pass — it's regular
content stream text.

---

## 13. Configuration knobs

Under `consumer.ocr` (`internal/config/config.go`):

| Key | Default | Meaning |
|---|---|---|
| `engine` | `gosseract` | `gosseract` (built-in) or `ocrmypdf` |
| `languages` | `[eng]` | plus-separated tesseract codes (`eng+spa`) |
| `workers` | — | OCR worker count for multi-page files (clamped to pages × 2×CPU) |
| `data_dir` | `<config-dir>/tessdata` | tessdata download location |
| `timeout` | — | per-document OCR budget (context-cancelled) |
| `optimizer` | `gs` or `mupdf` | `pdfoptimizer.engine` — which clean pass runs after OCR |

Languages are validated at setup (`kushim setup --languages eng,spa,...`),
and `consumer.ocr.languages` is required before consumption runs.

---

## 14. Gotchas

- **Pixmap lifecycle**: `RenderPage` returns a copied `samples` plus a live
  `pixmap` that must be freed via `adapters.FreePixmap` — free it right
  after enqueue, or RSS grows per page (`standalone.go:82,143`).
- **One Tesseract client per worker, never shared** — gosseract clients are
  not safe for concurrent use; the per-worker `newOCRClient` is deliberate.
- **Worker count clamp** (`min(ocrWorkers, numPages, NumCPU()*2)`) — a config
  of 64 workers on a 2-page PDF spawns 2; on a 4-core machine never more
  than 8.
- **`sync.Once` poisons tessdata on failure** — a transient download error
  fails every OCR task in the process until restart. The enricher's
  fire-and-forget download mitigates this at setup time.
- **`TESSDATA_PREFIX` is process-global** — setting it affects any other
  Tesseract use in the process; there is only one consumer, so that's fine,
  but beware if you add a second.
- **Output resolution ≠ OCR resolution** — OCR runs at 200 DPI, the embedded
  image is 150 DPI. Changing `outputDPI` changes file size and page-point
  math; changing `ocrDPI` changes recognition quality. Keep the two
  constants apart.
- **`pdf.SetTextRenderingMode(3)` once per page, not per word** — fpdf only
  emits `Tr` from `SetTextRenderingMode` itself; `SetFont`, `Text` and the
  `q`/`Q` transform blocks never reset it, so the mode set before the word
  loop persists for the whole page. No re-emit is needed inside the
  `TransformBegin`/`TransformEnd` blocks (`q`/`Q` save and restore the
  graphics state anyway).
- **JPEG quality 60 for the visible image** is a size/quality tradeoff; the
  invisible layer is what matters for search, so the image can be aggressive.
- **Temporary files** (`ocr_<uuid>.pdf` in `os.TempDir()`) are removed on
  every error path and after optimization — a leaked temp PDF is usually a
  missed `os.Remove` in a new early-return.
- **`--clean`/`unpaper` failures in ocrmypdf** come with hints, not just
  errors (`ocrmypdf.go:47-66`) — when adding engines, copy this pattern.
- **The 100 MP render cap and 50 MP image cap are DoS guards** — never raise
  them without also bounding the allocation somewhere else.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*
