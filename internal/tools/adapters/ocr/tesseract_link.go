//go:build cgo

package ocr

/*
#cgo LDFLAGS: -Wl,-Bstatic -L${SRCDIR}/../../../../build/tesseract/local/lib -L${SRCDIR}/../../../../build/leptonica/local/lib64 -L${SRCDIR}/../../../../build/libpng/local/lib64 -ltesseract -lleptonica -lpng -Wl,-Bdynamic -ldl
#cgo CFLAGS: -I${SRCDIR}/../../../../build/tesseract/local/include -I${SRCDIR}/../../../../build/libpng/local/include
*/
import "C"
