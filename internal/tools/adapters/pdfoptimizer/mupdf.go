//go:build cgo

package pdfoptimizer

/*
#include <stdlib.h>
#include <setjmp.h>

// --- MuPDF exception macros (from mupdf/fitz/system.h + mupdf/fitz/context.h) ---
// We can't #include <mupdf/fitz.h> because the include path is set by
// go-fitz's package, not ours. Instead, we forward-declare the internal
// functions that the macros expand to. These symbols are exported from
// libmupdf (already linked via go-fitz) and work on our opaque pointer.

typedef struct fz_context fz_context;

// fz_push_try returns a jmp_buf pointer.
jmp_buf *fz_push_try(fz_context *ctx);
int fz_do_try(fz_context *ctx);
int fz_do_always(fz_context *ctx);
int fz_do_catch(fz_context *ctx);

// These macros match MuPDF's public headers (context.h + system.h).
#define fz_try(ctx) if (!sigsetjmp(*fz_push_try(ctx), 0)) if (fz_do_try(ctx)) do
#define fz_always(ctx) while (0); if (fz_do_always(ctx)) do
#define fz_catch(ctx) while (0); if (fz_do_catch(ctx))

// --- pdf_write_options (from mupdf/pdf/document.h, MuPDF 1.24.9) ---
typedef struct {
	int do_incremental;
	int do_pretty;
	int do_ascii;
	int do_compress;
	int do_compress_images;
	int do_compress_fonts;
	int do_decompress;
	int do_garbage;
	int do_linear;
	int do_clean;
	int do_sanitize;
	int do_appearance;
	int do_encrypt;
	int dont_regenerate_id;
	int permissions;
	char opwd_utf8[128];
	char upwd_utf8[128];
	int do_snapshot;
	int do_preserve_metadata;
	int do_use_objstms;
	int compression_effort;
} pdf_write_options;

// --- pdf_image_rewriter_options (from mupdf/pdf/image-rewriter.h) ---
typedef struct {
	int color_lossless_image_subsample_method;
	int color_lossy_image_subsample_method;
	int color_lossless_image_subsample_threshold;
	int color_lossless_image_subsample_to;
	int color_lossy_image_subsample_threshold;
	int color_lossy_image_subsample_to;
	int color_lossless_image_recompress_method;
	int color_lossy_image_recompress_method;
	char *color_lossy_image_recompress_quality;
	char *color_lossless_image_recompress_quality;
	int gray_lossless_image_subsample_method;
	int gray_lossy_image_subsample_method;
	int gray_lossless_image_subsample_threshold;
	int gray_lossless_image_subsample_to;
	int gray_lossy_image_subsample_threshold;
	int gray_lossy_image_subsample_to;
	int gray_lossless_image_recompress_method;
	int gray_lossy_image_recompress_method;
	char *gray_lossy_image_recompress_quality;
	char *gray_lossless_image_recompress_quality;
	int bitonal_image_subsample_method;
	int bitonal_image_subsample_threshold;
	int bitonal_image_subsample_to;
	int bitonal_image_recompress_method;
	char *bitonal_image_recompress_quality;
} pdf_image_rewriter_options;

// --- pdf_clean_options (from mupdf/pdf/clean.h) ---
typedef struct {
	pdf_write_options write;
	pdf_image_rewriter_options image;
	int subset_fonts;
} pdf_clean_options;

// --- MuPDF library functions (declared for linking) ---
fz_context *fz_new_context_imp(void *alloc, void *locks, unsigned long max_store, const char *version);
void fz_register_document_handlers(fz_context *ctx);
void fz_drop_context(fz_context *ctx);
void pdf_clean_file(fz_context *ctx, char *infile, char *outfile,
	char *password, pdf_clean_options *opts, int retainlen, char *retainlist[]);

// Safe C wrapper: catches MuPDF exceptions in C land so longjmp never
// crosses the CGo boundary.
static int clean_file_safe(fz_context *ctx, char *infile, char *outfile,
	pdf_clean_options *opts) {
	fz_try(ctx) {
		pdf_clean_file(ctx, infile, outfile, NULL, opts, 0, NULL);
	}
	fz_catch(ctx) {
		return 0;
	}
	return 1;
}
*/
import "C"
import (
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// MuPDF implements PdfOptimizer using MuPDF's pdf_clean_file via CGo.
// No external binary required — MuPDF is already statically linked via go-fitz.
type MuPDF struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewMuPDF(logger *utils.Logger, cfg config.ToolConfig) (*MuPDF, error) {
	return &MuPDF{logger: logger, config: cfg}, nil
}

func (m *MuPDF) Name() string {
	return "mupdf"
}

// Optimize runs MuPDF's pdf_clean_file on the given PDF. The cleaning
// matches the Ghostscript /ebook preset: compress streams, garbage collect,
// deduplicate objects, subset fonts, clean/sanitize content streams,
// downsample images (colour/grayscale →150 DPI JPEG, mono →300 DPI FAX),
// and use object streams.
func (m *MuPDF) Optimize(path string) (*string, error) {
	tmpDir := os.TempDir()
	ogName := filepath.Base(path)
	ext := filepath.Ext(ogName)
	baseName := ogName[:len(ogName)-len(ext)]
	outputName := fmt.Sprintf("mupdf_%s_%d.pdf", baseName, time.Now().Unix())
	outputPath := filepath.Join(tmpDir, outputName)

	// --- Create MuPDF context ---
	cVersion := C.CString("1.24.9")
	defer C.free(unsafe.Pointer(cVersion))

	ctx := C.fz_new_context_imp(nil, nil, C.ulong(256<<20), cVersion)
	if ctx == nil {
		return nil, fmt.Errorf("mupdf: cannot create context")
	}
	defer C.fz_drop_context(ctx)

	C.fz_register_document_handlers(ctx)

	// --- Configure clean options ---
	// These map to: mutool clean -g -gg -ggg -c -i -z -l output
	// and approximate Ghostscript's -dPDFSETTINGS=/ebook

	// Quality strings for image recompression (must be C strings, freed below)
	cJpegQuality := C.CString("85")
	defer C.free(unsafe.Pointer(cJpegQuality))

	opts := &C.pdf_clean_options{}
	opts.write.do_garbage = 3         // compact xref + deduplicate objects
	opts.write.do_compress = 1        // flate-compress all streams
	opts.write.do_compress_images = 1 // compress image streams
	opts.write.do_compress_fonts = 1  // compress font streams
	opts.write.do_clean = 1           // clean content streams
	opts.write.do_sanitize = 1        // sanitize content streams
	// do_linear disabled — causes realloc crash on some PDFs in MuPDF 1.24.9
	// opts.write.do_linear = 1
	opts.write.do_use_objstms = 1       // use object streams (compacts xref)
	opts.write.do_preserve_metadata = 1 // keep document metadata
	// Font subsetting disabled (MuPDF 1.24.9 crashes on certain TTFs with
	// "TTF subsetting; gid >= num_gids!" — heap corruption in font subsetter).
	// Fonts are still compressed via do_compress_fonts above.
	// opts.subset_fonts = 1

	// --- Image rewriter options (match Ghostscript /ebook preset) ---
	// Threshold must be >0 (MuPDF checks threshold != 0 internally; 0 disables).
	// Images at or below threshold DPI are kept; above threshold are downsampled.
	// Lossy colour images: bicubic downsample to 150 DPI, recompress as JPEG @85
	opts.image.color_lossy_image_subsample_method = 1 // FZ_SUBSAMPLE_BICUBIC
	opts.image.color_lossy_image_subsample_threshold = 72
	opts.image.color_lossy_image_subsample_to = 150
	opts.image.color_lossy_image_recompress_method = 3 // FZ_RECOMPRESS_JPEG
	opts.image.color_lossy_image_recompress_quality = cJpegQuality

	// Lossless colour images: same treatment (downsampling destroys losslessness)
	opts.image.color_lossless_image_subsample_method = 1 // FZ_SUBSAMPLE_BICUBIC
	opts.image.color_lossless_image_subsample_threshold = 72
	opts.image.color_lossless_image_subsample_to = 150
	opts.image.color_lossless_image_recompress_method = 3 // FZ_RECOMPRESS_JPEG
	opts.image.color_lossless_image_recompress_quality = cJpegQuality

	// Lossy grayscale: bicubic downsample to 150 DPI, recompress as JPEG @85
	opts.image.gray_lossy_image_subsample_method = 1 // FZ_SUBSAMPLE_BICUBIC
	opts.image.gray_lossy_image_subsample_threshold = 72
	opts.image.gray_lossy_image_subsample_to = 150
	opts.image.gray_lossy_image_recompress_method = 3 // FZ_RECOMPRESS_JPEG
	opts.image.gray_lossy_image_recompress_quality = cJpegQuality

	// Lossless grayscale: same treatment
	opts.image.gray_lossless_image_subsample_method = 1 // FZ_SUBSAMPLE_BICUBIC
	opts.image.gray_lossless_image_subsample_threshold = 72
	opts.image.gray_lossless_image_subsample_to = 150
	opts.image.gray_lossless_image_recompress_method = 3 // FZ_RECOMPRESS_JPEG
	opts.image.gray_lossless_image_recompress_quality = cJpegQuality

	// Bitonal (1-bit monochrome): bicubic downsample to 300 DPI, CCITT Fax
	opts.image.bitonal_image_subsample_method = 1 // FZ_SUBSAMPLE_BICUBIC
	opts.image.bitonal_image_subsample_threshold = 72
	opts.image.bitonal_image_subsample_to = 300
	opts.image.bitonal_image_recompress_method = 5 // FZ_RECOMPRESS_FAX

	// --- Run cleaning ---
	cInfile := C.CString(path)
	defer C.free(unsafe.Pointer(cInfile))
	cOutfile := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cOutfile))

	success := C.clean_file_safe(ctx, cInfile, cOutfile, opts)
	if success == 0 {
		return nil, fmt.Errorf("mupdf: pdf_clean_file failed for %s", path)
	}

	// Verify output file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("mupdf: output file was not created for %s", path)
	}

	m.logger.Debug(nil, "mupdf processed %s -> %s", path, outputPath)
	return &outputPath, nil
}
