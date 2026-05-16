//go:build integration

package consumption

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func writePDF(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 12)
	pdf.Cell(40, 10, "Hello, World!")
	if err := pdf.OutputFileAndClose(path); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

func TestConsumeEndToEnd(t *testing.T) {
	consumptionDir := t.TempDir()
	storageDir := t.TempDir()

	writePDF(t, consumptionDir, "doc.pdf")

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	schemaData, err := os.ReadFile("../../sql/schema.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := db.Exec(string(schemaData)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{
			ConsumptionDir: consumptionDir,
			StorageDir:     storageDir,
		},
		Consumer: config.ConsumerConfig{
			SupportedFiles:       []string{".pdf"},
			TextExtractor:        "mupdf",
			PdfOptimizer:         "mupdf",
			OCR:                  "gosseract",
			OCRLanguages:         []string{"eng"},
			OCRDataDir:           t.TempDir(),
			OptimizationFallback: "",
		},
	}

	logger := utils.NewLogger("info")
	c := NewConsumer(cfg, logger, db)

	if err := c.Consume(nil); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	processedDir := filepath.Join(storageDir, "2026")
	if _, err := os.Stat(processedDir); os.IsNotExist(err) {
		t.Error("expected processed files in storage dir")
	}

	ctx := context.Background()
	queries := database.NewQueries(db)

	docs, err := queries.ListDocuments(ctx, database.ListDocumentsParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}

	doc := docs[0]
	if doc.Title != "doc.pdf" {
		t.Errorf("expected title 'doc.pdf', got %q", doc.Title)
	}
	if doc.MimeType != "application/pdf" {
		t.Errorf("expected mime type 'application/pdf', got %q", doc.MimeType)
	}

	results, err := queries.SearchDocumentsFTS(ctx, `"Hello"`, 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 FTS result, got %d", len(results))
	}
	if results[0].ID != doc.ID {
		t.Errorf("FTS result ID %d != document ID %d", results[0].ID, doc.ID)
	}
}
