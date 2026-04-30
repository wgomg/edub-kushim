# Kushim — Document Management System

Headless REST API + CLI document management system optimized for large collections
(tens of thousands of documents including full books). OCR and text extraction are
Go‑native with statically‑linked C libraries; the binary has zero system dependencies
except Ghostscript (pending replacement).

**Status**: MVP functional. Document consumption pipeline with Go‑native OCR
(gosseract + Tesseract) and text extraction (go‑fitz + MuPDF). FTS5 full‑text search
implemented.

## Quick Start

```bash
# Prerequisites (build‑time only)
sudo zypper install gcc gcc-c++ make autoconf automake libtool ghostscript

# Clone and build static C dependencies (one time)
git clone <repo>
cd edub-kushim
make build-deps

# Build and run
cp config.example.yaml config.yaml
mkdir -p inbox storage data
make consume
```

Build: `make build`. The Makefile exports `CGO_ENABLED=1` and `CGO_CPPFLAGS`
automatically. Ghostscript is the only remaining external runtime dependency.

## CLI Commands

```bash
kushim version     # Show version
kushim consume     # Scan inbox → extract → OCR → store → index
kushim server      # Start HTTP API server
```

## API

```bash
GET /health
GET /api/v1/documents?limit=50&offset=0
GET /api/v1/documents/{id}
GET /api/v1/documents/search?q=terms&limit=50&offset=0
```

## Minimal Config

```yaml
app:
  environment: development
  log_level: debug

server:
  host: localhost
  port: 3000

database:
  type: sqlite
  path: ./data

storage:
  consumption_dir: ./inbox
  storage_dir: ./storage

consumer:
  supported_files: ['.pdf']
  textextractor: 'go-fitz' # Go‑native
  pdfoptimizer: 'gs' # Ghostscript (pending MuPDF replacement)
  ocr: 'gosseract' # Go‑native
  delete_original: false
```

## Further Reading

- [Architecture & Design](architecture.md) — pipeline, data models, storage, FTS, roadmap
- [Implementation Reference](reference.md) — file‑by‑file API, endpoints, DB schema, full config
- [Go‑Native Adapter Plan](go-native-adapters-plan.md) — OCR/text‑extraction migration details
- [Building Tesseract & Leptonica](build-leptonica-tesseract.md) — static C dependency build guide
