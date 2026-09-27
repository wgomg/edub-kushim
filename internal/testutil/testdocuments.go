package testutil

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// CreateMinimalDocx writes a minimal valid DOCX file to the given path with the specified text content.
func CreateMinimalDocx(t testing.TB, path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent dir for %s: %v", path, err)
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create docx: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	ct, err := zw.Create("[Content_Types].xml")
	if err != nil {
		t.Fatalf("failed to create [Content_Types].xml: %v", err)
	}
	if _, err := ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`)); err != nil {
		t.Fatalf("failed to write [Content_Types].xml: %v", err)
	}

	doc, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("failed to create word/document.xml: %v", err)
	}
	if _, err := doc.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>` + content + `</w:t></w:r></w:p>
  </w:body>
</w:document>`)); err != nil {
		t.Fatalf("failed to write word/document.xml: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close docx zip: %v", err)
	}
}

// CreateMinimalOdt writes a minimal valid ODT file to the given path with the specified text content.
func CreateMinimalOdt(t testing.TB, path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent dir for %s: %v", path, err)
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create odt: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	mime, err := zw.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatalf("failed to create mimetype: %v", err)
	}
	if _, err := mime.Write([]byte("application/vnd.oasis.opendocument.text")); err != nil {
		t.Fatalf("failed to write mimetype: %v", err)
	}

	doc, err := zw.Create("content.xml")
	if err != nil {
		t.Fatalf("failed to create content.xml: %v", err)
	}
	if _, err := doc.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:text>
      <text:p>` + content + `</text:p>
    </office:text>
  </office:body>
</office:document-content>`)); err != nil {
		t.Fatalf("failed to write content.xml: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close odt zip: %v", err)
	}
}
