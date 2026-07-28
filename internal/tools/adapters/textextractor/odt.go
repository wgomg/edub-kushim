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
)

type Odt struct {
	logger *utils.Logger
}

func NewOdt(logger *utils.Logger) *Odt {
	return &Odt{logger: logger}
}

func (o *Odt) CanHandle(mimeType string) bool {
	return mimeType == "application/vnd.oasis.opendocument.text"
}

func (o *Odt) Name() string {
	return config.TextExtractor.Odt
}

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
			rc.Close()
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
			se := xml.CopyToken(t).(xml.StartElement)
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
			ee := xml.CopyToken(t).(xml.EndElement)
			local := ee.Name.Local
			space := ee.Name.Space
			if space == odtNS && (local == "p" || local == "h") {
				inParagraph = false
			}
		}
	}
}
