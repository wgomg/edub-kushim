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
	"github.com/wgomg/edub-kushim/internal/utils"

	_mime "github.com/wgomg/edub-kushim/internal/mime"
)

type Docx struct {
	logger *utils.Logger
}

func NewDocx(logger *utils.Logger) *Docx {
	return &Docx{logger: logger}
}

func (d *Docx) CanHandle(mimeType string) bool {
	return mimeType == _mime.DOCX
}

func (d *Docx) Name() string {
	return config.TextExtractor.Docx
}

func (d *Docx) Extract(ctx context.Context, path string, _ string) (*string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("docx: open zip: %w", err)
	}
	defer r.Close()

	var documentXML []byte
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("docx: open word/document.xml: %w", err)
			}
			documentXML, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("docx: read word/document.xml: %w", err)
			}
			break
		}
	}
	if documentXML == nil {
		return nil, fmt.Errorf("docx: word/document.xml not found")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	text, err := extractDocxText(documentXML)
	if err != nil {
		return nil, fmt.Errorf("docx: %w", err)
	}

	return &text, nil
}

const docxNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

func extractDocxText(xmlData []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var buf strings.Builder
	inText := false

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
			se := xml.CopyToken(t).(xml.StartElement)
			local := se.Name.Local
			space := se.Name.Space

			if space == docxNS && local == "p" {
				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
			}
			if space == docxNS && local == "t" {
				inText = true
			}
		case xml.CharData:
			if inText {
				buf.Write(t)
			}
		case xml.EndElement:
			ee := xml.CopyToken(t).(xml.EndElement)
			local := ee.Name.Local
			space := ee.Name.Space
			if space == docxNS && local == "t" {
				inText = false
			}
		}
	}
}
