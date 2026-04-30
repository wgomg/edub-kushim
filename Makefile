# Makefile

# Paths
CURDIR        ?= $(abspath .)
BUILD_DIR     := $(CURDIR)/build
TESS_INCLUDE  := $(BUILD_DIR)/tesseract/local/include
TESS_LIB      := $(BUILD_DIR)/tesseract/local/lib64
LEP_LIB       := $(BUILD_DIR)/leptonica/local/lib64
BINARY        := ./dev/bin/kushim
TESSDATA      := internal/tools/adapters/ocr/tessdata/eng.traineddata

# Flags required by gosseract's C++ bridge
export CGO_ENABLED    := 1
export CGO_CPPFLAGS   := -I$(TESS_INCLUDE)

.PHONY: all build build-deps clean run consume

all: build

## Download Tesseract language data if not present
$(TESSDATA):
	@echo "Downloading eng.traineddata..."
	mkdir -p $(dir $@)
	wget -q -O $@ https://github.com/tesseract-ocr/tessdata/raw/main/eng.traineddata
	@echo "Done."

## Build the Go binary
build: $(TESSDATA)
	go build -o $(BINARY) ./cmd/kushim/main.go

## Build Leptonica and Tesseract from source (one-time setup)
build-deps: $(TESSDATA)
	cd $(BUILD_DIR)/leptonica && ./autogen.sh && \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/leptonica/local \
			--libdir=$(BUILD_DIR)/leptonica/local/lib \
			--without-libpng --without-libtiff --without-libwebp --without-libopenjpeg \
			--without-giflib --disable-programs && \
		make -j$(shell nproc) && make install
	cd $(BUILD_DIR)/tesseract && ./autogen.sh && \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/tesseract/local \
			--with-extra-libraries=$(BUILD_DIR)/leptonica/local/lib \
			--with-extra-includes=$(BUILD_DIR)/leptonica/local/include \
			--with-curl=no --with-archive=no --disable-openmp --disable-legacy --disable-graphics && \
		make -j$(shell nproc) && make install

## Run the consumer pipeline
consume: build
	$(BINARY) consume

## Delete the binary
clean:
	rm -f $(BINARY)
