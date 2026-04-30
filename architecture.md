# Architecture & Design

## Core Design Principles

- **Headless First**: API-driven with CLI interface; web UI as optional layer
- **Tool Agnostic**: Adapter pattern; OCR and text extraction migrated to Go‑native libraries
- **SQLite First**: Development-friendly with migration path to production databases
- **Fallback Processing**: Text extraction → OCR → text extraction pattern
- **Date-based Organization**: Temporal storage structure for scalability
- **Transaction Safety**: Coordinated database and file operations with rollback
- **Single Binary**: Static C dependencies (MuPDF, Tesseract, Leptonica) linked into the binary

## Implementation Status

### ✅ Complete

- HTTP Server with middleware (request ID, parameter parsing)
- Database layer with sqlc-generated type-safe queries
- Configuration system with YAML support
- Structured logging with request correlation
- Document API (list, get by ID)
- CLI framework with dependency injection
- Adapter pattern for OCR, text extraction, and PDF optimization
- Document consumption pipeline (scan → extract → OCR → store)
- Go‑native OCR via gosseract + statically-linked Tesseract/Leptonica
- Go‑native text extraction via go‑fitz + statically-linked MuPDF
- Embedded Tesseract language data (`eng.traineddata`)
- PPM image encoding for OCR (avoids libjpeg version conflicts with MuPDF)
- Searchable PDF generation with invisible text layer (text rendering mode 3)
- PDF optimization via Ghostscript
- Deferred cleanup of temporary files
- Full-text search (FTS5) with manual query layer

### 🚧 In Progress

- FTS search API endpoints
- FTS trigger integration testing
- Performance testing for large collections

### 📋 Planned

- Async task processing with queue
- ZincSearch integration
- LLM/semantic processing for tags/classification
- Authentication and user management
- Replace Ghostscript with MuPDF native optimizer

---

## Processing Pipeline

### 1. File Discovery

Scans `consumption_dir` for supported extensions, detects MIME type, computes MD5 and
SHA512 checksums in a single pass.

### 2. Duplicate Detection

MD5 checksum lookup → SHA512 verification. Skips processing on exact match.

### 3. Text Extraction

Two‑stage strategy:

**Primary**: `go‑fitz` (statically‑linked MuPDF) extracts embedded text. If text is
found, the PDF is optimized via Ghostscript and stored.

**Fallback**: `gosseract` (statically‑linked Tesseract) OCRs image‑only pages. Each
page is rendered at 300 DPI, OCR'd from PPM (avoids libjpeg conflicts), and a new
searchable PDF is built with `go‑pdf/fpdf`. The original image is placed as the full‑page
background and recognized words are overlaid using **text rendering mode 3** (`3 Tr`),
which makes text selectable/searchable in all PDF viewers without painting it.

### 4. Database Integration

Document record created via `CreateDocument`, ID obtained from `LastInsertId()`,
date‑based storage paths generated, paths updated via `UpdateDocumentPaths`. All
wrapped in a database transaction with rollback on file‑operation failure.

### 5. File Movement

- **OCR case**: Move OCR temp file → processed storage, copy original → original storage.
- **Text case**: Copy original → original storage, move Ghostscript‑optimized file → processed storage.

### 6. FTS Indexing

Automatic via SQLite triggers:
- `document_ai` — INSERT into `document_fts`
- `document_au` — UPDATE FTS index
- `document_ad` — DELETE from FTS index

Uses `unicode61` tokenizer for multi‑language support without language‑specific stemming.

---

## Storage Organization

```
storage/
├── originals/                    # Original files (copied)
│   └── 2024/03/19/1.pdf
└── 2024/                         # Processed files (OCR'd or optimized)
    └── 03/19/1.pdf
```

Date‑based (`year/month/day/documentID.ext`) to avoid "too many files in one directory"
at scale. Dual storage preserves originals alongside processed versions.

---

## Data Models

### Document (Primary Entity)

```go
type Document struct {
    ID             int64
    Title          string         // filename
    Md5Checksum    string         // fast duplicate lookup
    Sha512Checksum string         // secure uniqueness (UNIQUE constraint)
    MimeType       string
    FileSize       int64
    CreatedAt      sql.NullTime
    ModifiedAt     sql.NullTime
    DocumentTypeID sql.NullInt64
    OriginalPath   string
    StoragePath    string
    TextContent    sql.NullString // for FTS indexing
}
```

### File (Processing Transient)

```go
type File struct {
    Name                 string
    OriginalPath         string
    OCRTmpPath           *string        // OCR temp file
    OptimizedPdfTmpPath  *string        // Ghostscript temp file
    StorageProcessedPath *string
    StorageOriginalPath  *string
    MD5Checksum          string
    SHA512Checksum       string
    Text                 sql.NullString
    MimeType             string
    Date                 time.Time
    FileSize             int64
}
```

### Supporting Entities

- **Author** / **Tag**: Many‑to‑many via `document_author` and `document_tag` junction tables
- **DocumentType**: Categorization (invoice, contract, book, etc.)
- **Task**: Async processing tracking
- **User**: Future authentication

---

## Full-Text Search (FTS5)

SQLite FTS5 virtual table (`document_fts`) with `unicode61` tokenizer for multi‑language
support. BM25 relevance ranking and snippet highlighting.

- **Phrase search**: Exact match with quotation marks
- **Boolean operators**: AND, OR, NOT
- **Prefix search**: Wildcard partial‑term matching
- **Automatic sync**: Triggers keep FTS in sync with `document` table

Manual implementation in `internal/database/fts5.go` required because sqlc doesn't
support FTS5 syntax (`MATCH`, `bm25()`, `snippet()`).

---

## Key Design Decisions

| Decision | Why |
|----------|-----|
| PPM for OCR, JPEG for PDF | Two encodes for two consumers. PPM avoids the libjpeg version conflict between Leptonica (v8) and MuPDF (v90). JPEG for fpdf is pure Go. |
| Text rendering mode 3 (`3 Tr`) | PDF standard for invisible‑but‑selectable text. Works in all viewers. fpdf doesn't expose `Tr`, so injected via `RawWriteStr`. |
| Raw `BT…ET` blocks | fpdf negates Y coordinates, placing text off‑page. Emitting our own PDF operators gives full positioning control. |
| Static C libraries | Eliminates system dependencies (Tesseract, Leptonica, MuPDF). See `build-leptonica-tesseract.md`. |
| fpdf over MuPDF PDF creation | fpdf is pure Go; MuPDF's PDF writing API requires additional CGo. Trade‑off: fpdf's coordinate quirks needed workarounds. |

---

## Known Limitations

- **FTS5**: No CJK word segmentation; text duplicated in `document` and `document_fts` tables
- **PDF optimization**: Text‑PDFs only; Ghostscript is the last remaining external dependency
- **Build‑time**: Requires `gcc`, `make`, `autotools` for Leptonica/Tesseract compilation
- **Runtime**: Ghostscript still required (pending MuPDF replacement)
