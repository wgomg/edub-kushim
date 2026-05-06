//go:build cgo

package ocr

/*
#cgo LDFLAGS: -static -L${SRCDIR}/../../../../build/tesseract/local/lib64 -L${SRCDIR}/../../../../build/leptonica/local/lib64 -L${SRCDIR}/../../../../build/libpng/local/lib64 -ltesseract -lleptonica -lpng
#cgo CFLAGS: -I${SRCDIR}/../../../../build/tesseract/local/include -I${SRCDIR}/../../../../build/libpng/local/include
*/
import "C"
