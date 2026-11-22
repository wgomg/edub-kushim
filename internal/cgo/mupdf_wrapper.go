//go:build cgo

// Package cgo is the C wrapper layer of the binary: the native libraries
// linked at build time behind Go types, currently MuPDF for page rendering,
// text extraction and PDF rewriting. Two implementations exist, selected by
// the cgo build tag: the real wrapper in this file and stubs in
// mupdf_nocgo.go that fail with "not available" errors, so CGO_ENABLED=0
// builds (edub, the unit tests) still compile.
//
// A MuContext owns a MuPDF context with a capped object store and silenced
// warnings. MuPDF contexts are not thread-safe: a context must not be used
// concurrently, so callers open one per unit of work.
package cgo

/*
#cgo LDFLAGS: -L${SRCDIR}/../../build/mupdf/local/lib -lmupdf -lmupdf-third -lm
#cgo CFLAGS: -I${SRCDIR}/../../build/mupdf/local/include

#include <mupdf/fitz.h>
#include <mupdf/pdf.h>
#include <string.h>
#include <stdlib.h>

// mupdf_new_context creates a MuPDF context and registers document handlers.
// Returns 0 on success, non-zero on error.
// NOTE: Cannot use fz_try/fz_catch here — no context exists yet.
//
// Store size is capped at 256 MB to prevent unbounded memory growth when
// processing large documents with many embedded images (e.g. scanned books).
// FZ_STORE_UNLIMITED caused ~5 GB RSS for a 963-page image-based PDF.
//
// MuPDF's error/warning print callback is replaced with a no-op to suppress
// recoverable warnings (xref errors, bad patterns, bogus fonts) that pollute
// the Go logger output.
static void mupdf_null_print(void *user, const char *message) { (void)user; (void)message; }

static int mupdf_new_context(fz_context **out_ctx)
{
	fz_context *ctx;

	ctx = fz_new_context_imp(NULL, NULL, 256 << 20, FZ_VERSION);
	if (!ctx)
		return 1;

	fz_register_document_handlers(ctx);

	// Suppress MuPDF stderr output — recoverable format warnings are noise.
	ctx->error.print = mupdf_null_print;
	ctx->warn.print = mupdf_null_print;

	*out_ctx = ctx;
	return 0;
}

// mupdf_shrink_store evicts cached objects so the store retains roughly 1% of its cap. In-use objects
// are never evicted.
static int mupdf_shrink_store(fz_context *ctx)
{
	return fz_shrink_store(ctx, 1);
}

// mupdf_open_document opens a PDF file. Returns 0 on success, non-zero on error.
static int mupdf_open_document(fz_context *ctx, const char *path, fz_document **out_doc)
{
	fz_document *doc = NULL;

	fz_var(doc);

	fz_try(ctx) {
		doc = fz_open_document(ctx, path);
	}
	fz_catch(ctx) {
		return 1;
	}

	*out_doc = doc;
	return 0;
}

// mupdf_extract_page_text extracts text from a single page into a buffer.
// Returns 0 on success, non-zero on error. Caller must fz_drop_buffer the result.
static int mupdf_extract_page_text(fz_context *ctx, fz_document *doc,
                                   int page_num, fz_buffer **out_buf)
{
	fz_page *page = NULL;
	fz_stext_page *stext = NULL;
	fz_device *dev = NULL;
	fz_buffer *buf = NULL;

	fz_var(page);
	fz_var(stext);
	fz_var(dev);
	fz_var(buf);

	fz_try(ctx) {
		page = fz_load_page(ctx, doc, page_num);
		stext = fz_new_stext_page(ctx, fz_bound_page(ctx, page));
		dev = fz_new_stext_device(ctx, stext, NULL);
		fz_run_page(ctx, page, dev, fz_identity, NULL);
		fz_close_device(ctx, dev);
		buf = fz_new_buffer_from_stext_page(ctx, stext);
	}
	fz_always(ctx) {
		fz_drop_device(ctx, dev);
		fz_drop_stext_page(ctx, stext);
		fz_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		fz_drop_buffer(ctx, buf);
		return 1;
	}

	*out_buf = buf;
	return 0;
}

// maximum pixels (width * height) for a single rendered page. Guards against
// malicious/malformed PDFs declaring an extreme page size, which combined
// with a fixed rendering DPI would otherwise drive fz_new_pixmap_with_bbox to
// attempt an unbounded allocation (memory-exhaustion DoS) — page dimensions
// come straight from untrusted document input and aren't bounded upstream.
// 100 MP allows generous large-format pages (e.g. a ~40x60in poster at 200
// DPI) while keeping a single RGB buffer under ~300 MB.
#define MUPDF_MAX_RENDER_PIXELS (100000000LL)

// mupdf_device_bbox rounds a page's bounds in device space at the caller's scale. Every render path
// derives its irect here, so page measurement, full-page renders and tile renders can never disagree
// on rounding.
static fz_irect mupdf_device_bbox(fz_context *ctx, fz_page *page, fz_matrix ctm)
{
	return fz_round_rect(fz_transform_rect(fz_bound_page(ctx, page), ctm));
}

// mupdf_check_render_pixels enforces the render cap on a device-space irect, keeping pathological page
// dimensions from reaching fz_new_pixmap_with_bbox.
static void mupdf_check_render_pixels(fz_context *ctx, fz_irect bbox)
{
	long long rw = (long long)bbox.x1 - (long long)bbox.x0;
	long long rh = (long long)bbox.y1 - (long long)bbox.y0;
	if (rw <= 0 || rh <= 0 || rw * rh > MUPDF_MAX_RENDER_PIXELS) {
		fz_throw(ctx, FZ_ERROR_GENERIC, "page dimensions exceed render limit");
	}
}

// mupdf_render_page renders a page to an RGB pixmap at the given DPI.
// Returns 0 on success, non-zero on error. Caller must fz_drop_pixmap the result.
static int mupdf_render_page(fz_context *ctx, fz_document *doc,
                             int page_num, float dpi,
                             fz_pixmap **out_pixmap, int *out_w, int *out_h)
{
	fz_page *page = NULL;
	fz_pixmap *pixmap = NULL;
	fz_device *dev = NULL;
	fz_matrix ctm;

	fz_var(page);
	fz_var(pixmap);
	fz_var(dev);

	fz_try(ctx) {
		page = fz_load_page(ctx, doc, page_num);
		ctm = fz_scale(dpi / 72.0f, dpi / 72.0f);
		fz_irect bbox = mupdf_device_bbox(ctx, page, ctm);
		mupdf_check_render_pixels(ctx, bbox);

		pixmap = fz_new_pixmap_with_bbox(ctx, fz_device_rgb(ctx), bbox, NULL, 0);
		fz_clear_pixmap_with_value(ctx, pixmap, 0xFF);
		dev = fz_new_draw_device(ctx, ctm, pixmap);
		fz_run_page(ctx, page, dev, fz_identity, NULL);
		fz_close_device(ctx, dev);
	}
	fz_always(ctx) {
		fz_drop_device(ctx, dev);
		fz_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		fz_drop_pixmap(ctx, pixmap);
		return 1;
	}

	*out_pixmap = pixmap;
	*out_w = pixmap->w;
	*out_h = pixmap->h;
	return 0;
}

// mupdf_page_size reports a page's pixel dimensions at the given DPI without rasterizing it. The page-level
// render cap is enforced here, before any buffer exists; tile rendering never trips it per tile.
static int mupdf_page_size(fz_context *ctx, fz_document *doc,
                           int page_num, float dpi,
                           int *out_w, int *out_h)
{
	fz_page *page = NULL;

	fz_var(page);

	fz_try(ctx) {
		page = fz_load_page(ctx, doc, page_num);
		fz_matrix ctm = fz_scale(dpi / 72.0f, dpi / 72.0f);
		fz_irect bbox = mupdf_device_bbox(ctx, page, ctm);
		mupdf_check_render_pixels(ctx, bbox);

		*out_w = bbox.x1 - bbox.x0;
		*out_h = bbox.y1 - bbox.y0;
	}
	fz_always(ctx) {
		fz_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		return 1;
	}

	return 0;
}

// mupdf_render_tile renders one tile of a page at the given DPI. Tile coordinates are offsets into the
// page-bbox pixel space, with the origin at the top-left of the bounded page; the draw device clips
// every operation to the pixmap bbox, so only the tile's pixels are rasterized. out_ox/out_oy report
// the pixmap origin relative to the page bbox.
static int mupdf_render_tile(fz_context *ctx, fz_document *doc,
                             int page_num, float dpi,
                             int x0, int y0, int x1, int y1,
                             fz_pixmap **out_pixmap, int *out_w, int *out_h,
                             int *out_ox, int *out_oy)
{
	fz_page *page = NULL;
	fz_pixmap *pixmap = NULL;
	fz_device *dev = NULL;
	fz_matrix ctm;
	fz_irect full;

	fz_var(page);
	fz_var(pixmap);
	fz_var(dev);
	fz_var(full);

	fz_try(ctx) {
		page = fz_load_page(ctx, doc, page_num);
		ctm = fz_scale(dpi / 72.0f, dpi / 72.0f);
		full = mupdf_device_bbox(ctx, page, ctm);
		fz_irect req = fz_make_irect(full.x0 + x0, full.y0 + y0,
		                             full.x0 + x1, full.y0 + y1);
		fz_irect ib = fz_intersect_irect(full, req);
		mupdf_check_render_pixels(ctx, ib);

		pixmap = fz_new_pixmap_with_bbox(ctx, fz_device_rgb(ctx), ib, NULL, 0);
		fz_clear_pixmap_with_value(ctx, pixmap, 0xFF);
		dev = fz_new_draw_device(ctx, ctm, pixmap);
		fz_run_page(ctx, page, dev, fz_identity, NULL);
		fz_close_device(ctx, dev);
	}
	fz_always(ctx) {
		fz_drop_device(ctx, dev);
		fz_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		fz_drop_pixmap(ctx, pixmap);
		return 1;
	}

	*out_pixmap = pixmap;
	*out_w = pixmap->w;
	*out_h = pixmap->h;
	*out_ox = pixmap->x - full.x0;
	*out_oy = pixmap->y - full.y0;
	return 0;
}

// mupdf_pdf_clean_file rewrites a PDF file with the given clean options.
// Pass NULL for opts to use defaults (garbage collection + compression only).
// Returns 0 on success, non-zero on error.
static int mupdf_pdf_clean_file(fz_context *ctx, const char *input_path,
                                const char *output_path, pdf_clean_options *opts)
{
	fz_try(ctx) {
		pdf_clean_file(ctx, (char *)input_path, (char *)output_path,
		               NULL, opts, 0, NULL);
	}
	fz_catch(ctx) {
		return 1;
	}
	return 0;
}

// mupdf_join_pdfs appends every page of every chunk into one new document and saves it at out_path.
// Chunks are opened one at a time, and each gets a single graft map so shared objects (the embedded
// font, images) are copied once per chunk instead of once per page. Streams are written out raw; the
// caller's optimizer pass owns recompression. On failure, *out_failed_chunk names the failing chunk
// index, or -1 when the failure happened outside a chunk (destination creation or save step).
static int mupdf_join_pdfs(fz_context *ctx, const char *out_path,
                           const char **chunk_paths, int n_chunks,
                           int *out_failed_chunk)
{
	pdf_document *dst = NULL;
	pdf_document *src = NULL;
	pdf_graft_map *map = NULL;
	int i, p, pages;

	fz_var(dst);
	fz_var(src);
	fz_var(map);

	*out_failed_chunk = -1;

	fz_try(ctx) {
		dst = pdf_create_document(ctx);
		for (i = 0; i < n_chunks; i++) {
			*out_failed_chunk = i;
			src = pdf_open_document(ctx, chunk_paths[i]);
			map = pdf_new_graft_map(ctx, dst);
			pages = pdf_count_pages(ctx, src);
			for (p = 0; p < pages; p++)
				pdf_graft_mapped_page(ctx, map, -1, src, p);
			pdf_drop_graft_map(ctx, map);
			map = NULL;
			pdf_drop_document(ctx, src);
			src = NULL;
		}
		*out_failed_chunk = -1;
		pdf_save_document(ctx, dst, out_path, &pdf_default_write_options);
	}
	fz_always(ctx) {
		pdf_drop_graft_map(ctx, map);
		pdf_drop_document(ctx, src);
	}
	fz_catch(ctx) {
		pdf_drop_document(ctx, dst);
		return 1;
	}

	pdf_drop_document(ctx, dst);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// MuContext owns a MuPDF fz_context: an object store capped at 256 MB to
// bound memory growth on large scanned documents, and no-op error printing
// so recoverable MuPDF warnings do not pollute the log. A context is not
// thread-safe and must not be used after Close.
type MuContext struct {
	p *C.fz_context
}

// MuDocument wraps an opened fz_document. Its methods take the owning
// MuContext, and the document must be closed before that context is.
type MuDocument struct {
	p *C.fz_document
}

// Pixmap is a rendered page's pixel buffer as RenderPage hands it out;
// release it with FreePixmap once the samples are consumed.
type Pixmap struct {
	p *C.fz_pixmap
}

// CleanOptions carries the PDF rewrite options PdfCleanFile applies.
// Create one with NewCleanOptions; a nil *CleanOptions means MuPDF's
// defaults.
type CleanOptions struct {
	p *C.pdf_clean_options
}

// NewMuContext creates a fresh MuPDF context. Contexts are not shared:
// create one per unit of work and Close it when done. On non-cgo builds it
// always fails.
func NewMuContext() (*MuContext, error) {
	var ctx *C.fz_context
	if C.mupdf_new_context(&ctx) != 0 {
		return nil, fmt.Errorf("mupdf: failed to create context")
	}
	return &MuContext{p: ctx}, nil
}

// OpenMuDocument opens the document at path. The returned document must be
// Closed before the owning context.
func (c *MuContext) OpenMuDocument(path string) (*MuDocument, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var doc *C.fz_document
	if C.mupdf_open_document(c.p, cPath, &doc) != 0 {
		return nil, fmt.Errorf("mupdf: failed to open document: %s", path)
	}
	return &MuDocument{p: doc}, nil
}

// NumPages reports how many pages the document contains.
func (d *MuDocument) NumPages(ctx *MuContext) int {
	return int(C.fz_count_pages(ctx.p, d.p))
}

// ExtractPageText returns the text of one page, numbered from 0 as MuPDF
// counts pages. The error names the failing page.
func (d *MuDocument) ExtractPageText(ctx *MuContext, pageNum int) (string, error) {
	var buf *C.fz_buffer
	if C.mupdf_extract_page_text(ctx.p, d.p, C.int(pageNum), &buf) != 0 {
		return "", fmt.Errorf("mupdf: failed to extract text from page %d", pageNum)
	}
	defer C.fz_drop_buffer(ctx.p, buf)

	data := C.GoBytes(unsafe.Pointer(buf.data), C.int(buf.len))
	return string(data), nil
}

// RenderPage renders one page at the given DPI and returns its width and
// height in pixels plus a fresh RGB sample buffer. samples is an independent
// copy; the pixmap is owned by the caller and must be released with
// FreePixmap once rendering is done. Pages whose pixel count exceeds the
// render limit fail instead of allocating unboundedly.
func (d *MuDocument) RenderPage(ctx *MuContext, pageNum int, dpi float64) (width, height int, samples []byte, pixmap *Pixmap, err error) {
	var p *C.fz_pixmap
	var w, h C.int

	if C.mupdf_render_page(ctx.p, d.p, C.int(pageNum), C.float(dpi), &p, &w, &h) != 0 {
		return 0, 0, nil, nil, fmt.Errorf("mupdf: failed to render page %d", pageNum)
	}

	// Copy samples before dropping pixmap — this was the leak in go-fitz.
	samples = C.GoBytes(unsafe.Pointer(p.samples), C.int(p.w*p.h*C.int(p.n)))
	return int(w), int(h), samples, &Pixmap{p: p}, nil
}

// PageSizePixels reports the page's rendered size in pixels at the given DPI without rasterizing it.
// Pages whose pixel area exceeds the render cap fail with an error: this is the page-level guard for
// tiled rendering, since oversized pages never take the full-page render path.
func (d *MuDocument) PageSizePixels(ctx *MuContext, pageNum int, dpi float64) (width, height int, err error) {
	var w, h C.int

	if C.mupdf_page_size(ctx.p, d.p, C.int(pageNum), C.float(dpi), &w, &h) != 0 {
		return 0, 0, fmt.Errorf("mupdf: failed to measure page %d", pageNum)
	}
	return int(w), int(h), nil
}

// RenderTile renders one tile of a page at the given DPI and returns its width and height in pixels
// plus an independent RGB sample copy. x0, y0, x1, y1 are offsets from the page bbox origin in pixels
// at the given DPI; the returned ox, oy report the actual pixmap origin relative to that same bbox,
// clamped to the page. The C pixmap is released before returning, so samples are the only value the
// caller owns. Tiles over the render cap fail instead of allocating.
func (d *MuDocument) RenderTile(ctx *MuContext, pageNum int, dpi float64, x0, y0, x1, y1 int) (samples []byte, width, height, ox, oy int, err error) {
	var p *C.fz_pixmap
	var w, h, px, py C.int

	if C.mupdf_render_tile(ctx.p, d.p, C.int(pageNum), C.float(dpi),
		C.int(x0), C.int(y0), C.int(x1), C.int(y1), &p, &w, &h, &px, &py) != 0 {
		return nil, 0, 0, 0, 0, fmt.Errorf("mupdf: failed to render tile on page %d", pageNum)
	}
	defer C.fz_drop_pixmap(ctx.p, p)

	samples = C.GoBytes(unsafe.Pointer(p.samples), C.int(p.w*p.h*C.int(p.n)))
	return samples, int(w), int(h), int(px), int(py), nil
}

// FreePixmap releases a pixmap returned by RenderPage. Safe to call with a
// nil pixmap.
func FreePixmap(ctx *MuContext, pixmap *Pixmap) {
	if pixmap == nil {
		return
	}
	C.fz_drop_pixmap(ctx.p, pixmap.p)
}

// Close closes the document.
func (d *MuDocument) Close(ctx *MuContext) {
	C.fz_drop_document(ctx.p, d.p)
	d.p = nil
}

// Close closes the context.
func (c *MuContext) Close() {
	C.fz_drop_context(c.p)
	c.p = nil
}

// ShrinkStore evicts cached objects from the context's store down toward a small fraction of its cap,
// releasing C memory retained from finished work. In-use objects are never evicted. The context is not
// thread-safe, so call it only from the goroutine that owns it.
func (c *MuContext) ShrinkStore() {
	C.mupdf_shrink_store(c.p)
}

// PdfCleanFile rewrites a PDF, removing unused objects and optionally
// applying optimizations (image downsampling, recompression, font subsetting).
// Pass nil for opts to use MuPDF defaults.
func (c *MuContext) PdfCleanFile(inputPath, outputPath string, opts *CleanOptions) error {
	cIn := C.CString(inputPath)
	defer C.free(unsafe.Pointer(cIn))
	cOut := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cOut))

	var raw *C.pdf_clean_options
	if opts != nil {
		raw = opts.p
	}

	// the JPEG quality C strings allocated by NewCleanOptions are freed
	// here after the call
	var jpegQuality *C.char
	if raw != nil && raw.image.color_lossy_image_recompress_quality != nil {
		jpegQuality = raw.image.color_lossy_image_recompress_quality
	}

	rc := C.mupdf_pdf_clean_file(c.p, cIn, cOut, raw)

	if jpegQuality != nil {
		C.free(unsafe.Pointer(jpegQuality))
	}

	if rc != 0 {
		return fmt.Errorf("mupdf: pdf_clean_file failed")
	}
	return nil
}

// JoinPDFs appends every page of each chunk, in order, into one new PDF saved at outputPath. Chunks
// are opened and grafted one chunk at a time, and every chunk's objects are copied through a single
// graft map, so shared resources within a chunk are not duplicated per page. Streams are copied raw, with no
// recompression or cleaning applied, leaving that to the caller's optimizer pass. A failed save may
// leave a partial outputPath for the caller to remove.
func (c *MuContext) JoinPDFs(outputPath string, chunkPaths []string) error {
	if len(chunkPaths) == 0 {
		return fmt.Errorf("mupdf: no chunks to join")
	}

	cOut := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cOut))

	cPaths := make([]*C.char, len(chunkPaths))
	for i, p := range chunkPaths {
		cPaths[i] = C.CString(p)
	}
	defer func() {
		for _, p := range cPaths {
			C.free(unsafe.Pointer(p))
		}
	}()

	failed := C.int(-1)
	rc := C.mupdf_join_pdfs(c.p, cOut, (**C.char)(unsafe.Pointer(&cPaths[0])),
		C.int(len(chunkPaths)), &failed)
	if rc != 0 {
		if int(failed) >= 0 {
			return fmt.Errorf("mupdf: failed to join PDF chunk %d (%s)", int(failed), chunkPaths[failed])
		}
		return fmt.Errorf("mupdf: failed to create or save the joined PDF: %s", outputPath)
	}
	return nil
}

// NewCleanOptions returns a pdf_clean_options handle configured to match
// Ghostscript's -dPDFSETTINGS=/ebook: garbage collect, deduplicate, compress,
// clean/sanitize content streams, downsample colour/grayscale images to 150 DPI
// JPEG @85, and bitonal images to 300 DPI CCITT Fax.
//
// The returned handle owns a C string (JPEG quality) that is freed by
// PdfCleanFile after the call completes.
func NewCleanOptions() *CleanOptions {
	cJpegQuality := C.CString("85")

	opts := &C.pdf_clean_options{}
	opts.write.do_garbage = 3
	opts.write.do_compress = 1
	opts.write.do_compress_images = 1
	opts.write.do_compress_fonts = 1
	opts.write.do_clean = 1
	opts.write.do_sanitize = 1
	opts.write.do_use_objstms = 1
	opts.write.do_preserve_metadata = 1

	opts.image.color_lossy_image_subsample_method = C.FZ_SUBSAMPLE_BICUBIC
	opts.image.color_lossy_image_subsample_threshold = 72
	opts.image.color_lossy_image_subsample_to = 150
	opts.image.color_lossy_image_recompress_method = C.FZ_RECOMPRESS_JPEG
	opts.image.color_lossy_image_recompress_quality = cJpegQuality

	opts.image.color_lossless_image_subsample_method = C.FZ_SUBSAMPLE_BICUBIC
	opts.image.color_lossless_image_subsample_threshold = 72
	opts.image.color_lossless_image_subsample_to = 150
	opts.image.color_lossless_image_recompress_method = C.FZ_RECOMPRESS_JPEG
	opts.image.color_lossless_image_recompress_quality = cJpegQuality

	opts.image.gray_lossy_image_subsample_method = C.FZ_SUBSAMPLE_BICUBIC
	opts.image.gray_lossy_image_subsample_threshold = 72
	opts.image.gray_lossy_image_subsample_to = 150
	opts.image.gray_lossy_image_recompress_method = C.FZ_RECOMPRESS_JPEG
	opts.image.gray_lossy_image_recompress_quality = cJpegQuality

	opts.image.gray_lossless_image_subsample_method = C.FZ_SUBSAMPLE_BICUBIC
	opts.image.gray_lossless_image_subsample_threshold = 72
	opts.image.gray_lossless_image_subsample_to = 150
	opts.image.gray_lossless_image_recompress_method = C.FZ_RECOMPRESS_JPEG
	opts.image.gray_lossless_image_recompress_quality = cJpegQuality

	opts.image.bitonal_image_subsample_method = C.FZ_SUBSAMPLE_BICUBIC
	opts.image.bitonal_image_subsample_threshold = 72
	opts.image.bitonal_image_subsample_to = 300
	opts.image.bitonal_image_recompress_method = C.FZ_RECOMPRESS_FAX

	return &CleanOptions{p: opts}
}
