//go:build !cgo

package cgo

import "errors"

// Stub implementations for non-CGo builds. countPages() in the consumer package
// returns 0 when these fail, so ingestion is not blocked.

type MuContext struct{}

type MuDocument struct{}

// Pixmap is the non-cgo stand-in for a rendered page; rendering always
// fails without CGo, so no value of it is ever produced.
type Pixmap struct{}

// CleanOptions is the non-cgo stand-in for the PDF rewrite options.
type CleanOptions struct{}

func NewMuContext() (*MuContext, error) {
	return nil, errors.New("MuPDF not available without CGo")
}

func (c *MuContext) OpenMuDocument(path string) (*MuDocument, error) {
	return nil, errors.New("MuPDF not available without CGo")
}

func (d *MuDocument) NumPages(ctx *MuContext) int {
	return 0
}

func (d *MuDocument) ExtractPageText(ctx *MuContext, pageNum int) (string, error) {
	return "", errors.New("MuPDF not available without CGo")
}

func (d *MuDocument) RenderPage(ctx *MuContext, pageNum int, dpi float64) (width, height int, samples []byte, pixmap *Pixmap, err error) {
	return 0, 0, nil, nil, errors.New("MuPDF not available without CGo")
}

func FreePixmap(ctx *MuContext, pixmap *Pixmap) {}

func (d *MuDocument) Close(ctx *MuContext) {}

func (c *MuContext) Close() {}

func (c *MuContext) PdfCleanFile(inputPath, outputPath string, opts *CleanOptions) error {
	return errors.New("MuPDF not available without CGo")
}

func NewCleanOptions() *CleanOptions {
	return nil
}
