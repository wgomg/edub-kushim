package textextractor

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"

	_mime "github.com/wgomg/edub-kushim/internal/documents/mime"
)

// Odt reads ODT files: the OpenDocument XML inside the zip, rendered to
// newline-separated paragraph text.
type Odt struct {
	logger *log.Logger
}

// NewOdt builds the reader.
func NewOdt(logger *log.Logger) *Odt {
	return &Odt{logger: logger}
}

// CanHandle reports ODT content types.
func (o *Odt) CanHandle(mimeType string) bool {
	return mimeType == _mime.ODT
}

// Name identifies the reader.
func (o *Odt) Name() string {
	return config.TextExtractor.Odt
}

// Extract reads content.xml from the zip and returns its text runs, one
// line per paragraph and heading. The path must be absolute.
func (o *Odt) Extract(ctx context.Context, path string, _ string) (*string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("odt: open zip: %w", err)
	}
	defer r.Close()

	var contentXML []byte
	for _, f := range r.File {
		if f.Name == "content.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("odt: open content.xml: %w", err)
			}
			contentXML, err = io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, fmt.Errorf("odt: read content.xml: %w", err)
			}
			break
		}
	}
	if contentXML == nil {
		return nil, fmt.Errorf("odt: content.xml not found")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	text, err := extractOdtText(contentXML)
	if err != nil {
		return nil, fmt.Errorf("odt: %w", err)
	}

	return &text, nil
}

const odtNS = "urn:oasis:names:tc:opendocument:xmlns:text:1.0"

func extractOdtText(xmlData []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var buf strings.Builder
	inParagraph := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			return strings.TrimSpace(buf.String()), nil
		}
		if err != nil {
			return "", fmt.Errorf("xml decode: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			se, _ := xml.CopyToken(t).(xml.StartElement)
			local := se.Name.Local
			space := se.Name.Space

			if space == odtNS && (local == "p" || local == "h") {
				inParagraph = true
				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
			}
		case xml.CharData:
			if inParagraph {
				buf.Write(t)
			}
		case xml.EndElement:
			ee, _ := xml.CopyToken(t).(xml.EndElement)
			local := ee.Name.Local
			space := ee.Name.Space
			if space == odtNS && (local == "p" || local == "h") {
				inParagraph = false
			}
		}
	}
}
