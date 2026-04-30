//go:build cgo

package ocr

/*
#cgo LDFLAGS: -static -L${SRCDIR}/../../../../build/tesseract/local/lib64 -L${SRCDIR}/../../../../build/leptonica/local/lib64 -ltesseract -lleptonica
#cgo CFLAGS: -I${SRCDIR}/../../../../build/tesseract/local/include
*/
import "C"
