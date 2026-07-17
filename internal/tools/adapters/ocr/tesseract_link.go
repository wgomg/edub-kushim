//go:build cgo

package ocr

/*
#cgo LDFLAGS: -Wl,-Bstatic -L${SRCDIR}/../../../../build/tesseract/local/lib64 -L${SRCDIR}/../../../../build/leptonica/local/lib64 -L${SRCDIR}/../../../../build/libpng/local/lib64 -ltesseract -lleptonica -lpng -Wl,-Bdynamic -ldl
#cgo CFLAGS: -I${SRCDIR}/../../../../build/tesseract/local/include -I${SRCDIR}/../../../../build/leptonica/local/include -I${SRCDIR}/../../../../build/libpng/local/include

#include <leptonica/allheaders.h>

static void lept_null_stderr_handler(const char *msg) { (void)msg; }

static void suppress_leptonica_stderr(void) {
    leptSetStderrHandler(lept_null_stderr_handler);
}
*/
import "C"

func suppressLeptonicaStderr() {
	C.suppress_leptonica_stderr()
}
