package mime

import (
	"testing"
)

func TestIsPDF(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{PDF, true},
		{"application/PDF", false},
		{DOCX, false},
		{"", false},
	}
	for _, tt := range tests {
		got := IsPDF(tt.input)
		if got != tt.want {
			t.Errorf("IsPDF(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsImage(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"image/tiff", true},
		{"application/pdf", false},
		{"image/", true},
		{"IMAGE/png", false},
		{"", false},
	}
	for _, tt := range tests {
		got := IsImage(tt.input)
		if got != tt.want {
			t.Errorf("IsImage(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsOfficeDoc(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{DOCX, true},
		{ODT, true},
		{PDF, false},
		{PNG, false},
		{"", false},
	}
	for _, tt := range tests {
		got := IsOfficeDoc(tt.input)
		if got != tt.want {
			t.Errorf("IsOfficeDoc(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestExtensionFromMimeType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{PDF, ".pdf"},
		{DOCX, ".docx"},
		{ODT, ".odt"},
		{TIFF, ".tiff"},
		{JPEG, ".jpg"},
		{PNG, ".png"},
		{"unknown/type", ".pdf"},
	}
	for _, tt := range tests {
		got := ExtensionFromMimeType(tt.input)
		if got != tt.want {
			t.Errorf("ExtensionFromMimeType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMimeTypeFromExtension(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{".pdf", PDF},
		{".PDF", PDF},
		{".docx", DOCX},
		{".odt", ODT},
		{".tiff", TIFF},
		{".tif", TIFF},
		{".jpg", JPEG},
		{".jpeg", JPEG},
		{".png", PNG},
		{".txt", OctetStream},
		{"pdf", OctetStream},
	}
	for _, tt := range tests {
		got := MimeTypeFromExtension(tt.input)
		if got != tt.want {
			t.Errorf("MimeTypeFromExtension(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildExtensionSet(t *testing.T) {
	input := []string{".pdf", ".docx", ".PDF"}
	got := BuildExtensionSet(input)

	if !got[".pdf"] {
		t.Error("BuildExtensionSet missing .pdf")
	}
	if !got[".docx"] {
		t.Error("BuildExtensionSet missing .docx")
	}
	if len(got) != 2 {
		t.Errorf("BuildExtensionSet returned %d entries, want 2", len(got))
	}
}

func TestSupportedConstants(t *testing.T) {
	if PDF != "application/pdf" {
		t.Errorf("PDF = %q", PDF)
	}
	if DOCX != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("DOCX = %q", DOCX)
	}
	if ODT != "application/vnd.oasis.opendocument.text" {
		t.Errorf("ODT = %q", ODT)
	}
	if TIFF != "image/tiff" {
		t.Errorf("TIFF = %q", TIFF)
	}
	if JPEG != "image/jpeg" {
		t.Errorf("JPEG = %q", JPEG)
	}
	if PNG != "image/png" {
		t.Errorf("PNG = %q", PNG)
	}
	if ZIP != "application/zip" {
		t.Errorf("ZIP = %q", ZIP)
	}
	if OctetStream != "application/octet-stream" {
		t.Errorf("OctetStream = %q", OctetStream)
	}
}

func TestExtensionsFor(t *testing.T) {
	tests := []struct {
		mimeType string
		want     []string
	}{
		{PDF, []string{".pdf"}},
		{DOCX, []string{".docx"}},
		{ODT, []string{".odt"}},
		{PNG, []string{".png"}},
		{TIFF, []string{".tiff", ".tif"}},
		{JPEG, []string{".jpg", ".jpeg"}},
		{"unknown/type", nil},
	}
	for _, tt := range tests {
		got := ExtensionsFor(tt.mimeType)
		if len(got) != len(tt.want) {
			t.Errorf("ExtensionsFor(%q) returned %d extensions, want %d", tt.mimeType, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("ExtensionsFor(%q)[%d] = %q, want %q", tt.mimeType, i, got[i], tt.want[i])
			}
		}
	}
}

func TestSupportedSlice(t *testing.T) {
	if len(Supported) != 6 {
		t.Fatalf("Supported has %d entries, want 6", len(Supported))
	}

	for _, m := range Supported {
		if m.MimeType == "" {
			t.Error("empty MimeType in Supported")
		}
		if m.Extension == "" {
			t.Errorf("empty Extension for %s", m.MimeType)
		}
		if m.Label == "" {
			t.Errorf("empty Label for %s", m.MimeType)
		}
	}
}
