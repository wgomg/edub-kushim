# Go‑Native Adapter Implementation Plan

## Goal

Replace the three external CLI tools (`pdftotext`, `ocrmypdf`, `gs`) with Go‑native
libraries, resulting in a **single, portable binary** that requires no system‑level
installation from the end user.

| Tool                     | Status     |
| ------------------------ | ---------- |
| `pdftotext` → `go‑fitz`  | ✅ Done    |
| `ocrmypdf` → `gosseract` | ✅ Done    |
| `gs` → MuPDF optimiser   | ⬜ Pending |

---

## 1. OCR: `gosseract` (Tesseract) replaces `ocrmypdf`

### 1.1 What was built

A `Gosseract` adapter that:

1. **Renders** each PDF page to an image with `go‑fitz` (`doc.Image()` at 300 DPI).
2. **Encodes** the image to PPM (netpbm) for Tesseract — avoids libjpeg because
   Leptonica handles PPM natively without any external library.
3. **OCR** via `gosseract.SetImageFromBytes` + `GetBoundingBoxes(RIL_WORD)`.
4. **Builds a searchable PDF** with `go‑pdf/fpdf`:
   - JPEG‑encodes the same image for the full‑page background (fpdf only
     accepts JPEG/PNG; this path is pure Go, never touches Leptonica).
   - Overlays each recognized word at the correct position using text
     rendering mode 3 (`3 Tr` — invisible but selectable), injected via
     `RawWriteStr`.
   - Converts Tesseract's 300‑DPI pixel coordinates to 72‑DPI PDF points and
     flips Y from top‑left to bottom‑left origin.

### 1.2 Key design decisions

| Decision                             | Why                                                                                                                                                                   |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| PPM for OCR, JPEG for PDF background | Two encodes for two consumers. PPM avoids the libjpeg version conflict between Leptonica (v8) and MuPDF (v90). The JPEG path for fpdf is pure Go.                     |
| `3 Tr` via `RawWriteStr`             | fpdf doesn't expose text rendering mode. Injecting `q\n3 Tr` / `Q` is the standard PDF way to make text searchable but invisible — works in all viewers.              |
| Raw `BT…ET` blocks for text          | fpdf negates Y coordinates, placing text off‑page. Emitting our own `BT x y Td (word) Tj ET` gives full control over positioning.                                     |
| Static Tesseract + Leptonica         | See `build-leptonica-tesseract.md`. Compiled without PNG/TIFF/WebP/JPEG support; Leptonica only needs PPM (built‑in). Linked into the binary via `tesseract_link.go`. |

### 1.3 Tesseract language data

`eng.traineddata` is embedded with `//go:embed` and extracted at runtime to a
temporary directory. `TESSDATA_PREFIX` is set to that directory before the
first OCR call.

---

## 2. PDF Optimisation: MuPDF replaces Ghostscript

**Not yet implemented.** MuPDF can perform PDF optimisation (subsetting fonts,
compressing images, linearising) via its C API. Since `go‑fitz` already statically
links MuPDF, a small CGo helper can perform PDF rewriting. Ghostscript remains
the optimisation step for now.

---

## 3. Build system

A `Makefile` at the project root handles everything:

```bash
make             # build the binary
make build-deps  # one‑time: compile Leptonica + Tesseract static libs
make consume     # build + run the pipeline
make clean       # remove the binary
```

The flags `CGO_ENABLED=1` and `CGO_CPPFLAGS` are exported by the Makefile; no
manual flags needed.

---

## 4. End‑User Experience

After the Ghostscript step is also migrated:

1. Download a **single binary** (`kushim`).
2. Create a config file (`cp config.example.yaml config.yaml`) and directories (`mkdir -p inbox storage data`).
3. Run `kushim consume` — the entire pipeline runs with zero external
   dependencies (no Python, no system packages).
