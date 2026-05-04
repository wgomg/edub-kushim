package ocr

import (
	_ "embed"
)

// kushimFontData is a TrueType font with Unicode coverage (LiberationSans-Regular)
// used for rendering invisible text layers in searchable PDFs.
// Licensed under SIL Open Font License 1.1.
//
//go:embed kushim_font.ttf
var kushimFontData []byte
