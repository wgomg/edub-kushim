# Building Leptonica and Tesseract (Static)

Everything lives inside `build/` in the project root. Nothing touches `/usr/local`.

## Prerequisites (build‑time only)

```bash
# openSUSE
sudo zypper install gcc gcc-c++ make autoconf automake libtool

# Debian/Ubuntu equivalent
sudo apt install build-essential autoconf automake libtool
```

---

## 1. Clone the repositories

```bash
mkdir -p build
git clone https://github.com/DanBloomberg/leptonica.git build/leptonica
git clone https://github.com/tesseract-ocr/tesseract.git  build/tesseract
```

---

## 2. Build Leptonica (static, minimal)

```bash
cd build/leptonica

./autogen.sh
./configure \
    --disable-shared \
    --enable-static \
    --prefix="$(pwd)/local" \
    --libdir="$(pwd)/local/lib" \
    --without-libpng \
    --without-libtiff \
    --without-libwebp \
    --without-libopenjpeg \
    --without-giflib \
    --disable-programs

make -j$(nproc)
make install
```

### What each flag does

| Flag                               | Why                                                    |
| ---------------------------------- | ------------------------------------------------------ |
| `--disable-shared --enable-static` | We only want `.a` files for static linking.            |
| `--prefix="$(pwd)/local"`          | Installs into a local tree, never `/usr/local`.        |
| `--libdir="$(pwd)/local/lib"`      | Forces `lib/` even on systems that default to `lib64`. |
| `--without-libpng`                 | No PNG support; Tesseract receives PPM from us.        |
| `--without-libtiff`                | Same reason.                                           |
| `--without-libwebp`                | Same reason.                                           |
| `--without-libopenjpeg`            | Same reason.                                           |
| `--without-giflib`                 | Same reason.                                           |
| `--disable-programs`               | Skips CLI tools (not needed at link time).             |

Removing image format support avoids having to also statically link libpng,
libtiff, libwebp, etc.

---

## 3. Build Tesseract (static, minimal)

```bash
cd ../tesseract

./autogen.sh
./configure \
    --disable-shared \
    --enable-static \
    --prefix="$(pwd)/local" \
    --with-extra-libraries="$(pwd)/../leptonica/local/lib" \
    --with-extra-includes="$(pwd)/../leptonica/local/include" \
    --with-curl=no \
    --with-archive=no \
    --disable-openmp \
    --disable-legacy \
    --disable-graphics

make -j$(nproc)
make install
```

### What each flag does

| Flag                               | Why                                                                                    |
| ---------------------------------- | -------------------------------------------------------------------------------------- |
| `--disable-shared --enable-static` | Only static libraries.                                                                 |
| `--prefix="$(pwd)/local"`          | Local install tree.                                                                    |
| `--with-extra-libraries`           | Points to Leptonica's static libs.                                                     |
| `--with-extra-includes`            | Points to Leptonica's headers.                                                         |
| `--with-curl=no`                   | Disables libcurl (used for downloading PDFs from URLs; we process local files only).   |
| `--with-archive=no`                | Disables libarchive (used for compressed `.traineddata`; we embed a raw file instead). |
| `--disable-openmp`                 | Removes OpenMP dependency (avoids `libgomp`).                                          |
| `--disable-legacy`                 | Skips old OCR engine; only LSTM is needed.                                             |
| `--disable-graphics`               | Skips GUI components (ScrollView).                                                     |

### Result

After both builds:

```
build/
├── leptonica/
│   └── local/
│       ├── include/         ← Leptonica headers
│       └── lib64/           ← libleptonica.a (lib64 on openSUSE/RHEL)
└── tesseract/
    └── local/
        ├── include/         ← Tesseract headers
        ├── lib64/           ← libtesseract.a
        └── share/tessdata/  ← pdf.ttf (unused)
```

---

## 4. Link into the Go binary

CGo preamble at `internal/tools/adapters/ocr/tesseract_link.go`:

```go
//go:build cgo

package ocr

/*
#cgo LDFLAGS: -static -L${SRCDIR}/../../../../build/tesseract/local/lib64 -L${SRCDIR}/../../../../build/leptonica/local/lib64 -ltesseract -lleptonica
#cgo CFLAGS: -I${SRCDIR}/../../../../build/tesseract/local/include
*/
import "C"
```

### Build command

```bash
CGO_ENABLED=1 CGO_CPPFLAGS="-I$(pwd)/build/tesseract/local/include" go build -o ./dev/bin/kushim ./cmd/kushim/main.go
```

Or via the Makefile: `make build`.

---

## Note on `lib64` vs `lib`

`./configure --libdir=.../local/lib` requests `lib/`, but some distros (openSUSE,
RHEL) ignore this and install into `lib64/`. If your build lands in `lib/`, adjust
the `-L` paths in `tesseract_link.go` to match. The Makefile uses `lib64` by default.
