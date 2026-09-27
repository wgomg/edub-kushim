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
		fz_rect bounds = fz_bound_page(ctx, page);
		ctm = fz_scale(dpi / 72.0f, dpi / 72.0f);
		fz_irect bbox = fz_round_rect(fz_transform_rect(bounds, ctm));

		long long rw = (long long)bbox.x1 - (long long)bbox.x0;
		long long rh = (long long)bbox.y1 - (long long)bbox.y0;
		if (rw <= 0 || rh <= 0 || rw * rh > MUPDF_MAX_RENDER_PIXELS) {
			fz_throw(ctx, FZ_ERROR_GENERIC, "page dimensions exceed render limit");
		}

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
