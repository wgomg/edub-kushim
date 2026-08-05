# Developer Guide — cgo and the C Wrapper Layer

This guide explains how edub-kushim wraps C libraries (MuPDF, Tesseract,
Leptonica) with cgo: the `import "C"` preamble, the static C helper layer,
memory management across the boundary, the `!cgo` stub twin, and the build
machinery that makes it all link. It is the "how to touch the C code without
breaking things" guide.

Audience: developers who need to work in `mupdf_wrapper.go`,
`tesseract_link.go`, or any file gated by `//go:build cgo`. Companion:
`ocr-pipeline.md` (what the wrappers are used for) and
`golang.md` §18 (build tags overview).

---

## Table of contents

1. [Orientation](#1-orientation)
2. [The `#cgo` preamble](#2-the-cgo-preamble)
3. [`import "C"` mechanics](#3-import-c-mechanics)
4. [The C helper layer pattern](#4-the-c-helper-layer-pattern)
5. [Memory management across the boundary](#5-memory-management-across-the-boundary)
6. [Go types wrapping C pointers](#6-go-types-wrapping-c-pointers)
7. [The `!cgo` stub twin](#7-the-cgo-stub-twin)
8. [The `init()` factory swap](#8-the-init-factory-swap)
9. [Defense in depth: limits and stderr suppression](#9-defense-in-depth-limits-and-stderr-suppression)
10. [Build integration](#10-build-integration)
11. [Testing cgo code](#11-testing-cgo-code)
12. [Gotchas](#12-gotchas)

---

## 1. Orientation

Two binaries, two worlds:

- **`kushim`** is built with `CGO_ENABLED=1` and statically links MuPDF,
  Tesseract, and Leptonica from the `build/` tree.
- **`edub`** is built with `CGO_ENABLED=0` — pure Go. It never compiles the C
  files; it gets the stub twins instead, or talks to `kushim` over the Unix
  socket.

Files involved:

| File | Tag | Role |
|---|---|---|
| `internal/tools/adapters/mupdf_wrapper.go` | `//go:build cgo` | the real MuPDF wrapper (`import "C"`) |
| `internal/tools/adapters/mupdf_nocgo.go` | `//go:build !cgo` | stub twin with identical names |
| `internal/tools/adapters/ocr/tesseract_link.go` | `//go:build cgo` | static-link flags + Leptonica stderr shim |
| `internal/tools/adapters/ocr/gosseract.go`, `standalone.go`, `tagmatcher/hugot.go` | `//go:build cgo` | consumers of the wrappers |
| `build/` | — | vendored C sources, compiled by `make build-deps` |

The build rule is absolute: all `go build`/`go test` invocations go through
the Makefile, which always sets `-tags "XLA,ORT"` (from the Hugot dependency
chain) and the CGo environment; C deps must exist first (`make build-deps`).
Bare `go` commands are not supported. See `AGENTS.md`.

---

## 2. The `#cgo` preamble

The file starts with the build tag, then a **C comment block immediately
before `import "C"`** — that block IS the C code cgo compiles
(`mupdf_wrapper.go:1-173`):

```go
//go:build cgo

package adapters

/*
#cgo LDFLAGS: -L${SRCDIR}/../../../build/mupdf/local/lib -lmupdf -lmupdf-third -lm
#cgo CFLAGS: -I${SRCDIR}/../../../build/mupdf/local/include

#include <mupdf/fitz.h>
#include <mupdf/pdf.h>
#include <string.h>
#include <stdlib.h>
...
*/
import "C"
```

Three things to understand:

1. **`#cgo` directives inside the comment** configure the compiler/linker.
   `CFLAGS` adds include paths; `LDFLAGS` adds library paths and libraries.
2. **`${SRCDIR}`** expands to the directory of the `.go` file at build time —
   that's how the wrapper reaches `build/mupdf/local/lib` from
   `internal/tools/adapters/`. This is why `make build-deps` must run first:
   the paths point at artifacts that don't exist yet otherwise.
3. **The rest of the comment is ordinary C** — headers plus (crucially)
   helper functions defined here, which become callable from Go as
   `C.mupdf_new_context(...)` etc. Defining helpers in the preamble is the
   standard cgo pattern for wrapping APIs that use error callbacks or
   multiple outputs.

The Tesseract link file shows the static-link dance
(`ocr/tesseract_link.go:6-7`):

```go
#cgo LDFLAGS: -Wl,-Bstatic -L${SRCDIR}/../../../../build/tesseract/local/lib64 -L${SRCDIR}/../../../../build/leptonica/local/lib64 -L${SRCDIR}/../../../../build/libpng/local/lib64 -ltesseract -lleptonica -lpng -Wl,-Bdynamic -ldl
```

`-Wl,-Bstatic ... -ltesseract -lleptonica -lpng -Wl,-Bdynamic -ldl` — link the
three libraries **statically**, then flip back to dynamic for `-ldl`. Order
matters: libraries must follow their dependents (tesseract → leptonica →
png), and the `-Bdynamic` switch must come before `-ldl` or libdl would be
dragged in statically too.

---

## 3. `import "C"` mechanics

When cgo sees `import "C"`:

- The preamble is compiled into a hidden C object and linked into the Go
  binary (no separate shared library).
- Every C symbol becomes available as `C.<name>`; every C type as
  `C.<type>` (`C.fz_context`, `C.pdf_clean_options`, ...).
- C integer types get Go conversions via `int(C.int(...))` / `C.int(...)`;
  pointers appear as opaque `*C.<type>`.
- The import must be in its own group — `import "C"` cannot share a
  parenthesized block with regular imports (`mupdf_wrapper.go:173` is a
  standalone line).
- **The preamble is compiled with the `go` command's C compiler** (gcc/clang)
  — a syntax error inside the comment surfaces as a cgo error at build time.

Go code can call the helper functions directly:

```go
var ctx *C.fz_context
if C.mupdf_new_context(&ctx) != 0 {
	return nil, fmt.Errorf("mupdf: failed to create context")
}
```

Note passing `&ctx` — a Go pointer to a C pointer — which cgo translates
correctly for out-parameters (this is a C-allocated buffer, so it falls
outside the "Go pointers in C" restriction, see §12).

---

## 4. The C helper layer pattern

Raw MuPDF calls are wrapped in static C functions that do three jobs:
**translate errors to a return code**, **centralize cleanup**, and **hide
C-only idioms from Go**. The canonical example (`mupdf_wrapper.go:65-98`):

```c
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
```

The pattern, consistently applied:

- **`fz_var(x)` before `fz_try`** — tells MuPDF's longjmp-based exceptions
  that `x` must be reset if a `setjmp` happens mid-assignment. Omitting it
  for a variable assigned inside the try block is undefined behavior.
- **`fz_try` / `fz_always` / `fz_catch`** — the `fz_always` block runs on
  both success and failure and drops the intermediate objects; `fz_catch`
  drops the output buffer (the only thing not yet handed to the caller) and
  returns a status code.
- **`fz_catch` never propagates C exceptions into Go** — every helper returns
  `0`/non-`0`, and the Go side turns that into a `fmt.Errorf`. This is the
  core rule: **the C boundary converts exceptions into codes; Go converts
  codes into errors.**
- The two no-context cases (`mupdf_new_context`) can't use `fz_try` (no
  context exists yet) — noted in the comment at `mupdf_wrapper.go:16`.

---

## 5. Memory management across the boundary

C memory and Go memory are separate heaps. The rules the wrapper follows:

**Input strings: `C.CString` + `defer C.free`**

```go
func (c *MuContext) OpenMuDocument(path string) (*MuDocument, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	...
	if C.mupdf_open_document(c.p, cPath, &doc) != 0 {
		return nil, fmt.Errorf("mupdf: failed to open document: %s", path)
	}
	return &MuDocument{p: doc}, nil
}
```

`C.CString` allocates a NUL-terminated C copy — it **must** be freed, always
via `defer` immediately after allocation.

**Output data: `C.GoBytes` copies out, then the C object is dropped**

```go
func (d *MuDocument) ExtractPageText(ctx *MuContext, pageNum int) (string, error) {
	var buf *C.fz_buffer
	if C.mupdf_extract_page_text(ctx.p, d.p, C.int(pageNum), &buf) != 0 {
		return "", fmt.Errorf("mupdf: failed to extract text from page %d", pageNum)
	}
	defer C.fz_drop_buffer(ctx.p, buf)

	data := C.GoBytes(unsafe.Pointer(buf.data), C.int(buf.len))
	return string(data), nil
}
```

The Go string is a **copy**; the C buffer is dropped right after. Never keep
a Go slice pointing into C memory.

**The pixmap lifecycle — and the leak that taught the lesson**
(`mupdf_wrapper.go:228-246`):

```go
func (d *MuDocument) RenderPage(ctx *MuContext, pageNum int, dpi float64) (width, height int, samples []byte, pixmap *C.fz_pixmap, err error) {
	...
	// Copy samples before dropping pixmap — this was the leak in go-fitz.
	samples = C.GoBytes(unsafe.Pointer(p.samples), C.int(p.w*p.h*C.int(p.n)))
	return int(w), int(h), samples, p, nil
}

// FreePixmap drops a pixmap. Must be called after RenderPage.
func FreePixmap(ctx *MuContext, pixmap *C.fz_pixmap) {
	C.fz_drop_pixmap(ctx.p, pixmap)
}
```

`RenderPage` returns **both** the copied `samples` (Go-owned, safe) and the
live `pixmap` (C-owned) — the caller *must* call `FreePixmap` after it's
done with the samples. The comment documents that an earlier wrapper
(go-fitz) dropped the pixmap before copying, leaking the samples. The OCR
pipeline calls `FreePixmap` right after queueing the render job
(`ocr/standalone.go:82,143`).

**C-allocated options with an embedded C string**
(`mupdf_wrapper.go:291-340`): `NewCleanOptions` builds a
`*C.pdf_clean_options` and allocates a `C.CString("85")` for the JPEG
quality field; `PdfCleanFile` (which receives the struct) frees that string
after the call (`mupdf_wrapper.go:271-283`). The ownership contract is
documented in the doc comment — read it before reusing the options struct.

The one-rule summary: **every `C.CString`, `C.CBytes`, `C.malloc`, or
`fz_new_*` has exactly one owner, and the Go side frees it with `defer` or
explicitly documents who does.**

---

## 6. Go types wrapping C pointers

The Go API hides `*C.fz_context` behind typed structs
(`mupdf_wrapper.go:180-188`):

```go
type MuContext struct {
	p *C.fz_context
}

type MuDocument struct {
	p *C.fz_document
}
```

- Methods take the *other* handle explicitly (`d.ExtractPageText(ctx, ...)`)
  because the context outlives documents — MuPDF requires the context for
  every drop call.
- `Close` methods null the pointer after dropping
  (`mupdf_wrapper.go:249-258`) so double-close is a no-op instead of a
  double-free.
- Numeric conversions are explicit everywhere: `C.int(pageNum)`,
  `int(C.fz_count_pages(ctx.p, d.p))`, `C.float(dpi)` — Go and C `int` are
  not interchangeable.
- `RenderPage` returns five values (width, height, samples, pixmap, err) —
  an unusual signature that exists *because* of the ownership split in §5.

---

## 7. The `!cgo` stub twin

For every build without cgo, `mupdf_nocgo.go` (tag `//go:build !cgo`) provides
the **same API with relaxed signatures**:

```go
func (d *MuDocument) RenderPage(ctx *MuContext, pageNum int, dpi float64) (width, height int, samples []byte, pixmap any, err error) {
	return 0, 0, nil, nil, errors.New("MuPDF not available without CGo")
}
```

- `pixmap any` and `opts any` instead of `*C.fz_pixmap` / `*C.pdf_clean_options`
  — the stub must not reference C types that don't exist in a `CGO_ENABLED=0`
  build. Callers that pass the pixmap through (like the OCR code) must
  therefore also be cgo-gated or use `any`-safe handling.
- **Callers degrade gracefully**: `countPages()` returns 0 when the stub
  errors, and ingestion is not blocked (`mupdf_nocgo.go:3-5` comment). The
  `edub` binary simply can't render or OCR; it relies on `kushim` for that.

The two files must stay in sync manually — adding a method to the wrapper
without the stub breaks `CGO_ENABLED=0` builds.

---

## 8. The `init()` factory swap

The OCR factory needs to compile in both worlds. The default is a failing
function (`internal/tools/adapters/ocr/adapter.go:18-22`):

```go
// newGosseract is overridden by gosseract.go via init() when CGo is
// available. The default returns an error.
var newGosseract = func(...) (OCR, error) {
	return nil, fmt.Errorf("gosseract OCR requires CGo — rebuild with CGO_ENABLED=1 and install the Tesseract dev headers")
}
```

and the cgo-only file registers the real constructor at package load
(`ocr/gosseract.go:137-141`):

```go
func init() {
	newGosseract = func(...) (OCR, error) {
		return NewGosseract(...)
	}
}
```

Net effect: `NewOCR` compiles everywhere; it fails at *runtime* with a clear
message only if the cgo implementation was never linked in.

---

## 9. Defense in depth: limits and stderr suppression

The C layer carries safety rails that Go code can't easily enforce:

**The render cap** (`mupdf_wrapper.go:100-107, 132-134`) — page dimensions
come from untrusted PDF input, so the wrapper refuses to allocate absurd
pixmaps:

```c
#define MUPDF_MAX_RENDER_PIXELS (100000000LL)
...
if (rw <= 0 || rh <= 0 || rw * rh > MUPDF_MAX_RENDER_PIXELS) {
	fz_throw(ctx, FZ_ERROR_GENERIC, "page dimensions exceed render limit");
}
```

100 MP keeps a single RGB buffer under ~300 MB while allowing ~40×60 in
posters at 200 DPI.

**The store cap** (`mupdf_wrapper.go:18-24`) — `fz_new_context_imp` gets a
256 MB cache store; the comment documents that `FZ_STORE_UNLIMITED` caused
~5 GB RSS on a 963-page scanned book.

**stderr suppression** — MuPDF's recoverable warnings would pollute Go logs,
so the print callbacks are nulled (`mupdf_wrapper.go:25, 37-39`), and
Leptonica's stderr handler is swapped via a small C shim
(`tesseract_link.go:11-15`):

```c
static void lept_null_stderr_handler(const char *msg) { (void)msg; }

static void suppress_leptonica_stderr(void) {
	leptSetStderrHandler(lept_null_stderr_handler);
}
```

called as `C.suppress_leptonica_stderr()` from `RunStandalone`
(`ocr/standalone.go:38`). Rationale: format warnings like
`pixReadMemTiff: function not present` are recoverable and would otherwise
flood the logs (see `ocr-pipeline.md` §8 for the stderr
tolerance list).

---

## 10. Build integration

`make build-deps` compiles the C trees (`build/mupdf`, `build/tesseract`,
`build/leptonica`, `build/libpng`) into `local/` prefix dirs — exactly where
the `${SRCDIR}`-relative `-L`/`-I` flags point. Key facts:

- The Makefile builds both binaries with the right CGO settings
  (`CGO_ENABLED=1` for kushim, `CGO_ENABLED=0` for edub) and always with
  `-tags "XLA,ORT"`.
- **Cache key** for CI's C-deps cache is `hashFiles('Makefile')` + arch —
  touching the Makefile invalidates the cache (see `AGENTS.md` release
  gotchas).
- Containerized builds (`make build-glibc`, `build-musl`) exist because the
  C toolchains are heavy; musl needs a builder image.
- The `-tags "XLA,ORT"` requirement is unrelated to these files — it comes
  from the Hugot/ONNX dependency chain.

If you add a new C dependency: build it into `build/<name>/local`, add the
`-L`/`-I` flags in the preamble, and make sure `make build-deps` produces
the artifacts.

---

## 11. Testing cgo code

Tests that touch the wrappers carry `//go:build cgo` and only run with the
toolchain installed:

- `internal/tools/adapters/mupdf_wrapper_test.go` — builds a minimal PDF in
  memory **with computed xref byte offsets** (`minimalPDF(t, w, h)`) to
  exercise the render-limit guard without fixture files.
- `internal/tools/adapters/ocr/gosseract_test.go`, `downscale_test.go` —
  OCR + image pipeline tests.
- `internal/tools/adapters/tagmatcher/hugot_test.go` — model tests.
- `internal/commands` — no `//go:build cgo` tag of its own, but the package
  only compiles with CGo because `hugot.go` imports the cgo-gated tagmatcher
  adapter. It is tested under `make test-cgo`, where it links natively; its
  tests themselves are pure Go (config handler, snippet highlighting).

`make test` runs `CGO_ENABLED=0` and therefore skips all of these
automatically. There is no mockable seam for `import "C"` — if you need to
test error paths, keep the Go side thin (thin wrappers = thin tests) and
exercise the C logic through the public Go API.

---

## 12. Gotchas

- **The `//go:build` line must be followed by a blank line** before
  `package`; a missing blank line makes the tag part of the package comment
  and both files compile → duplicate symbols.
- **`import "C"` must be standalone** — not in a parenthesized import block,
  and no other imports on the same line.
- **Never return a Go slice/string pointing into C memory.** Always
  `C.GoBytes`/`C.GoString` copy, then drop the C object. The `go-fitz` leak
  comment at `mupdf_wrapper.go:238` is the canonical cautionary tale.
- **Every `C.CString` needs its `defer C.free` immediately** — leaks are
  invisible (C heap) and don't show in Go's profiler.
- **Go pointers must not be stored in C** (cgo pointer rules). The wrappers
  only pass *C-allocated* memory to C — keep it that way. `&ctx` out-params
  are fine because cgo treats them as transient.
- **`fz_var` is mandatory** for any local assigned inside `fz_try` that is
  read after a potential longjmp; omitting it is UB that manifests as
  double-free or use-after-free on malformed PDFs.
- **Keep wrapper and stub twins in sync** — new methods on
  `mupdf_wrapper.go` break `edub` until `mupdf_nocgo.go` gains them (with
  relaxed `any` signatures).
- **`${SRCDIR}` is resolved at build time** — moving a wrapper file breaks
  the `-L`/`-I` paths silently (link errors with confusing messages).
- **Static link order matters** (`-Wl,-Bstatic ... -Wl,-Bdynamic -ldl`):
  libraries after their dependents; the `-Bdynamic` flip must precede any
  dynamic lib.
- **C warnings don't go through Go's logger** — that's why the suppression
  shims exist; if you see C stderr noise, extend the shim, don't filter it
  in Go.
- **cgo is not free**: crossing the boundary per page/word costs; batch C
  work in the helper functions (the OCR page loop renders and OCRs in bulk,
  `ocr/standalone.go`) rather than calling tiny C functions per word.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*
