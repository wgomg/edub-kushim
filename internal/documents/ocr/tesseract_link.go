//go:build cgo

package ocr

/*
#cgo LDFLAGS: -Wl,-Bstatic -L${SRCDIR}/../../../build/tesseract/local/lib64 -L${SRCDIR}/../../../build/leptonica/local/lib64 -L${SRCDIR}/../../../build/libpng/local/lib64 -ltesseract -lleptonica -lpng -Wl,-Bdynamic -ldl
#cgo CFLAGS: -I${SRCDIR}/../../../build/tesseract/local/include -I${SRCDIR}/../../../build/leptonica/local/include -I${SRCDIR}/../../../build/libpng/local/include

#include <leptonica/allheaders.h>

#ifdef __GLIBC__
#include <malloc.h>
#endif

static void lept_null_stderr_handler(const char *msg) { (void)msg; }

static void suppress_leptonica_stderr(void) {
    leptSetStderrHandler(lept_null_stderr_handler);
}

static void trim_process_heap(void) {
#ifdef __GLIBC__
    malloc_trim(0);
#endif
}
*/
import "C"

func suppressLeptonicaStderr() {
	C.suppress_leptonica_stderr()
}

// trimProcessHeap asks glibc to release free heap pages back to the OS; it is a no-op on musl and other
// libc implementations.
func trimProcessHeap() {
	C.trim_process_heap()
}
