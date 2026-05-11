//go:build cgo

package adapters

/*
#cgo LDFLAGS: -L${SRCDIR}/../../../build/mupdf/local/lib -lmupdf -lmupdf-third -lm
#cgo CFLAGS: -I${SRCDIR}/../../../build/mupdf/local/include

#include <mupdf/fitz.h>
#include <mupdf/pdf.h>
#include <string.h>
#include <stdlib.h>

// mupdf_new_context creates a MuPDF context and registers document handlers.
// Returns 0 on success, non-zero on error.
// NOTE: Cannot use fz_try/fz_catch here — no context exists yet.
static int mupdf_new_context(fz_context **out_ctx)
{
	fz_context *ctx;

	ctx = fz_new_context_imp(NULL, NULL, FZ_STORE_UNLIMITED, FZ_VERSION);
	if (!ctx)
		return 1;

	fz_register_document_handlers(ctx);
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

// mupdf_pdf_clean_file rewrites a PDF file, removing unused objects.
// Returns 0 on success, non-zero on error.
static int mupdf_pdf_clean_file(fz_context *ctx, const char *input_path,
                                const char *output_path)
{
	fz_try(ctx) {
		pdf_clean_file(ctx, (char *)input_path, (char *)output_path,
		               NULL, NULL, 0, NULL);
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

// MuContext wraps a MuPDF fz_context pointer.
type MuContext struct {
	p *C.fz_context
}

// MuDocument wraps a MuPDF fz_document pointer.
type MuDocument struct {
	p *C.fz_document
}

// NewMuContext creates a new MuPDF context.
func NewMuContext() (*MuContext, error) {
	var ctx *C.fz_context
	if C.mupdf_new_context(&ctx) != 0 {
		return nil, fmt.Errorf("mupdf: failed to create context")
	}
	return &MuContext{p: ctx}, nil
}

// OpenMuDocument opens a PDF file and returns a document handle.
func (c *MuContext) OpenMuDocument(path string) (*MuDocument, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var doc *C.fz_document
	if C.mupdf_open_document(c.p, cPath, &doc) != 0 {
		return nil, fmt.Errorf("mupdf: failed to open document: %s", path)
	}
	return &MuDocument{p: doc}, nil
}

// NumPages returns the number of pages in the document.
func (d *MuDocument) NumPages(ctx *MuContext) int {
	return int(C.fz_count_pages(ctx.p, d.p))
}

// ExtractPageText extracts text from a single page.
func (d *MuDocument) ExtractPageText(ctx *MuContext, pageNum int) (string, error) {
	var buf *C.fz_buffer
	if C.mupdf_extract_page_text(ctx.p, d.p, C.int(pageNum), &buf) != 0 {
		return "", fmt.Errorf("mupdf: failed to extract text from page %d", pageNum)
	}
	defer C.fz_drop_buffer(ctx.p, buf)

	data := C.GoBytes(unsafe.Pointer(buf.data), C.int(buf.len))
	return string(data), nil
}

// RenderPage renders a page to RGB pixel data at the given DPI.
// Returns width, height, and raw RGB samples (caller must free via FreePixmap).
func (d *MuDocument) RenderPage(ctx *MuContext, pageNum int, dpi float64) (width, height int, samples []byte, pixmap *C.fz_pixmap, err error) {
	var p *C.fz_pixmap
	var w, h C.int

	if C.mupdf_render_page(ctx.p, d.p, C.int(pageNum), C.float(dpi), &p, &w, &h) != 0 {
		return 0, 0, nil, nil, fmt.Errorf("mupdf: failed to render page %d", pageNum)
	}

	// Copy samples before dropping pixmap — this was the leak in go-fitz.
	samples = C.GoBytes(unsafe.Pointer(p.samples), C.int(p.w*p.h*C.int(p.n)))
	return int(w), int(h), samples, p, nil
}

// FreePixmap drops a pixmap. Must be called after RenderPage.
func FreePixmap(ctx *MuContext, pixmap *C.fz_pixmap) {
	C.fz_drop_pixmap(ctx.p, pixmap)
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

// PdfCleanFile rewrites a PDF, removing unused objects.
func (c *MuContext) PdfCleanFile(inputPath, outputPath string) error {
	cIn := C.CString(inputPath)
	defer C.free(unsafe.Pointer(cIn))
	cOut := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cOut))

	if C.mupdf_pdf_clean_file(c.p, cIn, cOut) != 0 {
		return fmt.Errorf("mupdf: pdf_clean_file failed")
	}
	return nil
}
