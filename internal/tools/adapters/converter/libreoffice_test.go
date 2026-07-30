package converter

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"

	_mime "github.com/wgomg/edub-kushim/internal/mime"
)

func TestLibreOfficeCanHandle(t *testing.T) {
	lo := &LibreOffice{}
	tests := []struct {
		mimeType string
		want     bool
	}{
		{_mime.DOCX, true},
		{_mime.ODT, true},
		{_mime.PDF, false},
		{_mime.PNG, false},
		{"", false},
	}
	for _, tt := range tests {
		got := lo.CanHandle(tt.mimeType)
		if got != tt.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tt.mimeType, got, tt.want)
		}
	}
}

func TestLibreOfficeName(t *testing.T) {
	lo := &LibreOffice{}
	if got := lo.Name(); got != "libreoffice" {
		t.Errorf("Name() = %q, want %q", got, "libreoffice")
	}
}

func TestNewLibreOfficeNotFound(t *testing.T) {
	_, err := NewLibreOffice(nil, config.ToolConfig{}, "nonexistent-binary-12345")
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
}
