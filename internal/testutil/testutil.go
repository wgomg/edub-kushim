package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// NewTestLogger creates a discard logger for tests.
func NewTestLogger() *utils.Logger {
	return utils.NewDiscardLogger()
}

// NewTestConfig creates a Config for testing with temporary directories.
// Returns the config and a cleanup function that removes the temp directory.
func NewTestConfig(t interface{ Fatalf(format string, args ...any) }) (*config.Config, func()) {
	configDir, err := os.MkdirTemp("", "edub-kushim-test-*")
	if err != nil {
		t.Fatalf("failed to create temp config dir: %v", err)
	}

	inboxDir := filepath.Join(configDir, "inbox")
	storageDir := filepath.Join(configDir, "storage")
	dataDir := filepath.Join(configDir, "data")
	ocrDataDir := filepath.Join(configDir, "ocr", "tessdata")

	for _, d := range []string{inboxDir, storageDir, dataDir, ocrDataDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			os.RemoveAll(configDir)
			t.Fatalf("failed to create dir %s: %v", d, err)
		}
	}

	cfg := config.DefaultConfig(configDir)
	cfg.Storage.ConsumptionDir = inboxDir
	cfg.Storage.StorageDir = storageDir
	cfg.Consumer.OCR.DataDir = ocrDataDir
	cfg.Consumer.OCR.Languages = []string{"eng"}
	cfg.Consumer.TextExtractor.Engine = config.TextExtractor.GoPdf
	cfg.Consumer.TextExtractor.Timeout = 30
	cfg.Consumer.PdfOptimizer.Engine = config.PdfOptimizer.MuPDF
	cfg.Consumer.PdfOptimizer.Timeout = 30
	cfg.Consumer.OCR.Engine = config.OCR.Gosseract
	cfg.Consumer.OCR.Timeout = 30
	cfg.App.LogLevel = "silent"
	cfg.Enricher.Workers = 0
	cfg.Consumer.Workers = 0

	cleanup := func() {
		os.RemoveAll(configDir)
	}

	return cfg, cleanup
}

// MinimalTextPDF returns a byte slice containing a minimal valid PDF with text content.
func MinimalTextPDF(content string) []byte {
	content = strings.ReplaceAll(content, `\`, `\\`)
	content = strings.ReplaceAll(content, `(`, `\(`)
	content = strings.ReplaceAll(content, `)`, `\)`)
	content = strings.ReplaceAll(content, "\n", `\n`)

	pdf := `%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj

2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj

3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]
   /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>
endobj

4 0 obj
<< /Length 44 >>
stream
BT /F1 12 Tf 100 700 Td (` + content + `) Tj ET
endstream
endobj

5 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj

xref
0 6
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
0000000266 00000 n 
0000000361 00000 n 
trailer
<< /Size 6 /Root 1 0 R >>
startxref
411
%%EOF`
	return []byte(pdf)
}

// CreateTestPDF writes a minimal PDF to the specified path.
func CreateTestPDF(t interface{ Fatalf(format string, args ...any) }, path, content string) {
	data := MinimalTextPDF(content)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write test PDF: %v", err)
	}
}

// CreateTestFile writes data to the given path.
func CreateTestFile(t interface{ Fatalf(format string, args ...any) }, path, content string) {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
}

// FormatString is a convenience wrapper around fmt.Sprintf.
func FormatString(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// AssertEqual checks that two values have equal string representations.
func AssertEqual(t interface{ Fatalf(format string, args ...any) }, got, want any, msg string) {
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Fatalf("%s: got %v, want %v", msg, got, want)
	}
}

// AssertNoError checks that an error is nil.
func AssertNoError(t interface{ Fatalf(format string, args ...any) }, err error, msg string) {
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", msg, err)
	}
}

// AssertError checks that an error is not nil.
func AssertError(t interface{ Fatalf(format string, args ...any) }, err error, msg string) {
	if err == nil {
		t.Fatalf("%s: expected error, got nil", msg)
	}
}
