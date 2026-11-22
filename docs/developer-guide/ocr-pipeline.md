# Developer Guide — The OCR Pipeline and Searchable PDFs

This guide explains how edub-kushim turns scanned PDFs and images into
searchable documents: the two OCR engines, the self-reexec subprocess
architecture, page rendering, the parallel worker pipeline, searchable-PDF
assembly with an invisible text layer, tessdata management, and the
post-OCR optimization step.

Audience: developers working on `internal/documents/ocr/`, or anyone
tuning OCR quality/performance. Companion: `cgo.md` (the
MuPDF/Tesseract wrapper layer underneath).

---

## Table of contents

1. [Orientation](#1-orientation)
2. [Two engines, one interface](#2-two-engines-one-interface)
3. [The self-reexec architecture](#3-the-self-reexec-architecture)
4. [Rendering pages with MuPDF](#4-rendering-pages-with-mupdf)
5. [The parallel tile pipeline](#5-the-parallel-tile-pipeline)
6. [Per-tile OCR: images and boxes](#6-per-tile-ocr-images-and-boxes)
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

Everything lives in `internal/documents/ocr/`:

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
// internal/documents/ocr/adapter.go:11-15
type OCR interface {
	Process(ctx context.Context, docId, path string) (*string, error)
	CanHandle(mimeType string) bool
	Name() string
}
```

`Process` returns the path of the produced searchable PDF (`*string`; nil on
failure). The factory (`adapter.go:24-33`) switches on the configured engine,
with `newGosseract` as the cgo-gated hook (§7 of the cgo guide):

```go
func NewOCR(logger *log.Logger, cfg config.ToolConfig, pdfOptimizerCmd string, languages []string, dataDir string, ocrWorkers int) (OCR, error) {
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
(`internal/documents/ocr/gosseract.go:56-65`):

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
cmd.Env = append(os.Environ(),
	"OMP_THREAD_LIMIT=1",             // parallelism comes from the tile pool, not OpenMP
	"MALLOC_ARENA_MAX=4",             // cap glibc arenas
	"MALLOC_MMAP_THRESHOLD_=1048576", // large tile buffers return to the OS on free
)
```

`os.Args[0]` is the running `kushim` binary; `cmd/kushim/main.go`
special-cases `internal-ocr` before normal command dispatch. Why this design:

- **Crash isolation** — Tesseract/Leptonica are C code; a segfault in the
  child kills only the child. The parent sees an exit error, cleans up, and
  the task fails gracefully instead of taking down the worker process.
- **Memory isolation** — the 2–5 GB class of RSS this work can reach is
  charged to the child, not the long-running daemon; the child also runs with
  `MALLOC_ARENA_MAX=4` and a fixed 1 MB mmap threshold, so large tile buffers
  are returned on free instead of being retained in per-thread arenas.
- **Simple parallelism** — the child owns all worker goroutines for
  multi-page files.

The parent streams the child's stdout line-by-line into its own logger
(`gosseract.go:80-85`):

```go
go func() {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		o.logger.Info(docId, "%s", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		o.logger.Warn(docId, "read OCR subprocess stdout: %v", err)
	}
}()
```

and the child prints progress lines that become log output: a startup line
(`"starting OCR on %d pages with %d workers"`), periodic `"OCR page 50/200"`
lines, and a final `"ocr done: pages …, workers …, peak RSS …"` line built
from `VmHWM` in `/proc/self/status` — the measurement hook for comparing OCR
settings across runs. A stdout read error is logged as a warning. The context
passed to `exec.CommandContext` cancels the child on timeout or shutdown.

---

## 4. Rendering pages with MuPDF

Inside the child, every page is measured first — a bounds-only probe that
allocates nothing — and then rendered at **200 DPI** through the cgo wrapper:

```go
w, h, err := doc.PageSizePixels(mupdfCtx, i, ocrDPI)
specs, err := planTiles(w, h)
...
samples, tw, th, ox, oy, err := doc.RenderTile(mupdfCtx, i, ocrDPI,
	spec.render.Min.X, spec.render.Min.Y, spec.render.Max.X, spec.render.Max.Y)
```

`planTiles` splits the page into a row-major grid of tiles whose **cores**
partition the page exactly:

- **Tile budget**: a page at or under `tileMaxPixels` (4,000,000 px, about one
  A4 page at 200 DPI) is a single tile whose core is the whole page — one
  render, one OCR pass, one JPEG.
- **Tiling**: larger pages split each axis at an even split rounded up to a
  multiple of 4 and capped at 2000 px (the square root of the budget), so
  every tiled core stays within the budget and at least 4 px wide. When the
  last span alone would be under 4 px, it borrows 4 px from its neighbour.
- **Overlap**: each tile's *render* rectangle is its core expanded by
  `tileOverlapPx` (128 px, 16 mm at 200 DPI) on every side, clamped at the
  page edges. The overlap exists for OCR only; placed images cover the cores
  (§7).
- **Caps**: `tileMaxPerPage` (256) rejects degenerate strip pages, and the
  document total is capped at `tileMaxPerDoc` (100,000) so tile plans and
  result buffers stay proportional to real work. The page-level
  `MUPDF_MAX_RENDER_PIXELS` guard (100 MP) runs in the probe, before any
  buffer exists; a per-tile check backs it in the C helper (`cgo.md` §8).
- `ocrDPI` (200) is the OCR resolution — Tesseract's quality sweet spot.
- `outputDPI` (150) is the resolution of the *image embedded in the output
  PDF* — smaller files, still readable. It no longer affects page geometry:
  page points always derive from the 200 DPI pixel grid (§7).
- `RenderTile` copies the tile's samples to Go and drops the pixmap before
  returning, so the caller owns only the sample slice.

Single-page files run the probe → plan → render loop sequentially; multi-page
files feed the parallel pipeline (§5).

---

## 5. The parallel tile pipeline

For `numPages > 1`, tiles are OCR'd by a worker pool:

```go
numWorkers := min(ocrWorkers, totalTiles, runtime.GOMAXPROCS(0)*2)
if numPages <= 1 {
	numWorkers = 1                      // the single-page path runs sequentially
}

jobs := make(chan tileJob, min(2, numWorkers))  // page/tile index, core, samples, dims, origin
results := make(chan tileResult, maxChunkTiles) // boxes + JPEG per tile, or an error
var wg sync.WaitGroup

for range numWorkers {
	wg.Go(func() {
		worker := newOCRClient(languages)     // one Tesseract client per worker
		defer worker.Close()
		for job := range jobs {
			samples := job.samples
			job.samples = nil                 // the queued job stops pinning the render
			boxes, jpegData, err := computeTile(samples, job.tw, job.th,
				job.core, job.ox, job.oy, worker)
			...
		}
	})
}
```

- **Probe pass first**: every page is measured and planned before rendering;
  a guard failure fails the document before any pixmap exists.
- **Worker sizing**: `ocr_workers: 0` derives the pool from `GOMAXPROCS(0)`
  — cgroup CPU-quota aware on Linux — divided by
  `max_concurrent_batches × consumer.workers`; the result is clamped to the
  document's tile count and `2 × GOMAXPROCS`. Explicit values are honored up
  to the same clamp. Single-page files never build the pool.
- **One Tesseract client per worker** — gosseract clients are not
  concurrency-safe, so each worker owns its own.
- **Chunk-synchronous production**: pages are grouped into chunks
  (`planChunks`: 64 pages or 256 MP of summed page pixels, whichever comes
  first), and the main goroutine works one chunk at a time. It renders that
  chunk's pages into `jobs` (a shallow 1–2-slot buffer) while the workers OCR,
  then drains exactly the chunk's tile results into chunk-local slots, and
  only then moves to the next chunk. `RenderTile` releases each pixmap
  internally; the producer closes `jobs`, a closer goroutine closes `results`
  after `wg.Wait()`.
- **Shallow job queue**: each queued job retains its rendered tile (3 bytes
  per pixel), and the producer is throttled by OCR anyway, so a deeper buffer
  would only retain memory.
- **Bounded retention**: the results channel is sized to the largest chunk's
  tile count, so workers never block sending while the producer is still
  pushing jobs for that chunk — that cap, not the jobs buffer, is what
  prevents the send-side deadlock. Only one chunk's boxes and JPEGs are
  retained at a time.
- **Chunk-boundary release**: after each chunk's PDF is written, the child
  evicts MuPDF's cache store (`MuContext.ShrinkStore`), calls `malloc_trim(0)`
  on glibc builds, and runs `debug.FreeOSMemory()`. Nothing from the finished
  chunk is referenced at that point, so eviction is free; these calls control
  the release rate, not the per-tile working set.
- **Deterministic output**: results land in per-chunk slots indexed by page and
  tile, and pages are assembled in document order with tiles row-major —
  concurrent OCR, sequential PDF.
- Render errors abort the run (`runErr`), and any tile error fails the whole
  run as `page N: tile M: …` — an OCR failure on page 12 of a book is a failed
  task, not a partial PDF.

---

## 6. Per-tile OCR: images and boxes

`computeTile` produces everything one output tile needs:

```go
ocrImg := samplesToGray(samples, tw, th)                  // 8-bit Rec.601 luma
pngData, err := encodePNG(ocrImg)                         // the whole overlapped render
ocrImg = nil

outW := tw * outputDPI / ocrDPI
outH := th * outputDPI / ocrDPI
lowSamples := downscaleRGB(samples, tw, th, outW, outH)   // nearest-neighbor
samples = nil
lowImg := samplesToRGBA(lowSamples, outW, outH)           // for the output JPEG
lowSamples = nil
coreLocal := core.Sub(image.Pt(ox, oy))                   // the core, in tile coordinates
coreLow := image.Rect(                                    // the core mapped to 150 DPI
	coreLocal.Min.X*outputDPI/ocrDPI, coreLocal.Min.Y*outputDPI/ocrDPI,
	coreLocal.Max.X*outputDPI/ocrDPI, coreLocal.Max.Y*outputDPI/ocrDPI,
).Intersect(image.Rect(0, 0, outW, outH))
jpegData, err := encodeJPEG(lowImg.SubImage(coreLow), 60) // core crop only
lowImg = nil

client.SetImageFromBytes(pngData)
pngData = nil
boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD) // word-level boxes
// keep a box iff its centre lies in coreLocal; offset kept boxes by (ox, oy)
```

- Tesseract gets a **lossless 8-bit grayscale PNG** (Rec.601 luma, 1 byte per
  pixel) at 200 DPI of the whole overlapped tile (compression artifacts hurt
  recognition); the visible image in the output is a **JPEG at quality 60**,
  150 DPI — OCR input and display image are deliberately different. Tesseract
  converts RGB input to gray internally with the same weights, so the
  recognition input is equivalent within ±1 rounding.
- **Buffer lifetimes**: the JPEG and low-res image are encoded before
  recognition and each large Go buffer is nil'ed once consumed, so only the
  PNG is live during `GetBoundingBoxes` — the multi-second call where
  Tesseract's pix and page structures peak.
- **Ownership rule**: a word box belongs to the tile whose core contains the
  word's centre (`coreLocal`), so words seen by two overlapping renders are
  counted exactly once; kept boxes are translated to page pixels. With the
  128 px overlap, words up to about 32 mm wide are fully visible in the tile
  that owns them.
- **Crop-core**: the JPEG is encoded from the core sub-rectangle of the
  downscaled tile (`SubImage`), so placed images partition the page without
  double-painted overlap. A core that maps to an empty crop — possible only
  for decoded images with an axis under 4 px — fails loudly instead of
  producing an invalid zero-dimension JPEG.
- `downscaleRGB` is a hand-rolled nearest-neighbor sampler — fast,
  allocation-light, good enough for a display image.

The Tesseract client is tuned for speed and raw boxes
(`newOCRClient`):

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

`addPDFPage` builds the output with go-pdf/fpdf:

```go
scale := 72.0 / ocrDPI
pdf.AddPageFormat("P", fpdf.SizeType{
	Wd: float64(res.w) * scale,
	Ht: float64(res.h) * scale,
})

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

desc := pdf.GetFontDesc("KushimText", "")  // Ascent/Descent in 1/1000 em
metrics := fontMetrics{ascent: float64(desc.Ascent), descent: float64(desc.Descent)}

pdf.SetTextRenderingMode(3)          // invisible text!
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
```

The pieces:

- **Page size in points** derives from the page's 200 DPI pixel grid
  (`w * 72 / 200`); image placement and the text layer share the same single
  `72/200` scale, so tile origins, image sizes and box positions stay in one
  coordinate system.
- **One image per tile**, named `kushim-page-<page>-<tile>`, placed at the
  tile's core origin and sized to the core: the images tile the page exactly.
- **`pdf.SetTextRenderingMode(3)`** — PDF text rendering mode 3 = "neither
  fill nor stroke": the text is **invisible** but present in the content
  stream, which is what makes the PDF searchable/selectable (this is the
  `3 Tr` mode mentioned in `AGENTS.md`).
- **Metrics-driven placement** (`wordPlacement`): each OCR box's pixel rect is
  scaled from 200 DPI pixels to page points; the font size is
  `height * 1000 / (Ascent - Descent)` so the glyph extent fills the box
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
  (`pdf.AddUTF8FontFromBytes("KushimText", "", kushimFontData)`) — Unicode
  coverage for any script the OCR produces, no system font dependency.

Each chunk is written as its own fpdf document. `newChunkPDF` creates the
instance and registers the embedded font; `addPDFPage` adds the chunk's pages
in document order; `OutputFileAndClose` writes the chunk to
`<os.TempDir()>/ocr_chunk_<doc-uuid>_<k>.pdf` and the instance is dropped. One
chunk's image map and serialized buffer are therefore the whole of the
builder's retention.

The final document is produced as follows:

- A multi-page document that produced one chunk is renamed to `outputPath`.
  If the rename fails across filesystems, the join below is used instead. The
  single-page and image paths write `outputPath` directly.
- Several chunks are joined by `MuContext.JoinPDFs` → `mupdf_join_pdfs`: a
  fresh destination comes from `pdf_create_document`, each chunk is opened in
  turn, one graft map per chunk appends its pages with
  `pdf_graft_mapped_page(…, -1, chunk, page)`, and the result is saved once
  with `pdf_save_document` and the default write options. The graft map
  deduplicates shared objects within a chunk, so the embedded font is copied
  once per chunk rather than once per page; streams pass through raw, and the
  optimizer (§12) still owns recompression.
- Chunk files are deleted after the join and on every failure path. A failed
  join also removes the partial `outputPath`, so a failed document leaves no
  PDF behind.

The join copies every grafted stream into the destination document's xref, so
final assembly holds about one copy of the output's stream bytes; the fpdf
builder and the tile results remain bounded by one chunk.

---

## 8. The image path

If the input is an image rather than a PDF (`runStandaloneImage`), the flow is
the same minus rendering: decode the image (`image.Decode` with tiff
registered via a blank import), enforce a **50 MP pixel cap** against
decompression bombs, convert to RGB, and wrap in a one-page searchable PDF:

```go
const maxPixels = 50_000_000
if int64(w)*int64(h) > maxPixels {
	return fmt.Errorf("image dimensions %dx%d (%d pixels) exceed maximum %d", w, h, w*h, maxPixels)
}
```

The whole image becomes a single tile (its core is the image), so it goes
through the same `computeTile` path. An image with an axis under 4 px fails
the empty-crop guard instead of emitting an invalid JPEG.

`imageToRGB` handles **premultiplied alpha** — the
standard `RGBA()` round trip `(r + 65535 - a) >> 8` un-premultiplies so white
backgrounds with alpha don't come out dark.

---

## 9. Tessdata management

Tesseract needs language data files. `EnsureLanguages`
(`internal/documents/ocr/tessdata.go:15-48`) downloads missing
`.traineddata` files from the `tessdata_fast` repo:

```go
var ensureOnce sync.Once

func EnsureLanguages(logger *log.Logger, dataDir string, languages []string) error {
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
  triggers the download (`ensureOCRLanguage`, `internal/enrichment/enricher.go:621-674`)
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
		o.logger.Warn(docId, "OCR subprocess had non-fatal errors: %v (stderr: %s)", err, stderr.String())
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

Both paths run the produced PDF — one file, whether renamed from a single
chunk or joined from several — through the optimizer
(`gosseract.go:104-111`):

```go
optResult, err := o.optimizer.Optimize(ctx, docId, outPath)
```

The optimizer is the `pdfoptimizer` tool: **Ghostscript** (`gs
-dPDFSETTINGS=/ebook ...`) or the **MuPDF clean path** (`mupdf_pdf_clean_file`
with `NewCleanOptions`, `mupdf_wrapper.go:338-381`), which replicates the
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
| `ocr_workers` | `0` (auto) | parallel tile workers for multi-page files; auto = cgroup-aware CPU count / (`max_concurrent_batches` × `consumer.workers`), clamped to 2×CPU and the tile count |
| `data_dir` | `<config-dir>/tessdata` | tessdata download location |
| `timeout` | — | per-document OCR budget (context-cancelled) |
| `optimizer` | `gs` or `mupdf` | `pdfoptimizer.engine` — which clean pass runs after OCR |

Languages are validated at setup (`kushim setup --languages eng,spa,...`),
and `consumer.ocr.languages` is required before consumption runs.

---

## 14. Gotchas

- **Pixmap lifecycle**: `RenderPage` (the thumbnail path) returns a copied
  `samples` plus a live `pixmap` that must be freed via
  `adapters.FreePixmap`; `RenderTile` (the OCR path) copies the tile samples
  and releases its pixmap internally — the caller owns only the sample slice.
- **One Tesseract client per worker, never shared** — gosseract clients are
  not safe for concurrent use; the per-worker `newOCRClient` is deliberate.
- **Worker count clamp** (`min(ocrWorkers, totalTiles, GOMAXPROCS(0)*2)`) — a
  config of 64 workers on a 2-page PDF spawns 2; a quota of 4 CPUs never runs
  more than 8.
- **Allocator tuning is per-child** — `MALLOC_ARENA_MAX` and
  `MALLOC_MMAP_THRESHOLD_` are set only on the `internal-ocr` child, and
  `malloc_trim` is a no-op outside glibc, so the release controls do nothing
  on musl builds.
- **`sync.Once` poisons tessdata on failure** — a transient download error
  fails every OCR task in the process until restart. The enricher's
  fire-and-forget download mitigates this at setup time.
- **`TESSDATA_PREFIX` is process-global** — setting it affects any other
  Tesseract use in the process; there is only one consumer, so that's fine,
  but beware if you add a second.
- **Output resolution ≠ OCR resolution** — OCR runs at 200 DPI, the embedded
  image is 150 DPI. Changing `outputDPI` changes the embedded image
  resolution and file size; page-point math always derives from the 200 DPI
  pixel grid. Changing `ocrDPI` changes recognition quality. Keep the two
  constants apart.
- **Seam coverage is bounded by the tile overlap** — `tileOverlapPx`
  (128 px, 16 mm at 200 DPI) keeps words up to about 32 mm wide intact across
  a tile edge; larger display type crossing a seam can be recognized as two
  fragments. Normal body text is unaffected.
- **`pdf.SetTextRenderingMode(3)` once per page, not per word** — fpdf only
  emits `Tr` from `SetTextRenderingMode` itself; `SetFont`, `Text` and the
  `q`/`Q` transform blocks never reset it, so the mode set before the word
  loop persists for the whole page. No re-emit is needed inside the
  `TransformBegin`/`TransformEnd` blocks (`q`/`Q` save and restore the
  graphics state anyway).
- **JPEG quality 60 for the visible image** is a size/quality tradeoff; the
  invisible layer is what matters for search, so the image can be aggressive.
- **Temporary files** (`ocr_<uuid>.pdf` and `ocr_chunk_<uuid>_<k>.pdf` in
  `os.TempDir()`) are removed on every error path, chunk files after assembly
  and the output file after optimization — a leaked temp PDF is usually a
  missed `os.Remove` in a new early-return.
- **`--clean`/`unpaper` failures in ocrmypdf** come with hints, not just
  errors (`ocrmypdf.go:47-66`) — when adding engines, copy this pattern.
- **The render caps and tile ceilings are DoS guards** — the 100 MP render
  cap, the 50 MP image cap, and `tileMaxPerPage`/`tileMaxPerDoc` all bound
  allocations that untrusted page dimensions would otherwise drive. Never
  raise them without also bounding the allocation somewhere else.

---

*Last verified against the tree: 2026-11-22. If code and doc disagree, code wins.*
