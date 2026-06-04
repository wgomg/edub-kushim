CURDIR        ?= $(abspath .)
BUILD_DIR     := $(CURDIR)/build
BINARY        := ./dev/bin/kushim
EDUB_BINARY   := ./dev/bin/edub
TESS_INCLUDE  := $(BUILD_DIR)/tesseract/local/include
TESS_LIB      := $(BUILD_DIR)/tesseract/local/lib64
LEP_LIB       := $(BUILD_DIR)/leptonica/local/lib64
MUPDF_DIR     := $(BUILD_DIR)/mupdf
MUPDF_LIB     := $(MUPDF_DIR)/local/lib
LIBNG_VER     := 1.6.43
MUPDF_VER     := 1.27.2
TOKENIZERS_DIR := $(BUILD_DIR)/tokenizers

# Flags required by gosseract's C++ bridge
export CGO_ENABLED  := 1
export CGO_CPPFLAGS := -I$(TESS_INCLUDE) -I$(BUILD_DIR)/leptonica/local/include -I$(BUILD_DIR)/libpng/local/include
export CGO_LDFLAGS  := -L$(TOKENIZERS_DIR)

# TEST_FLAGS — extra flags passed to `go test` (e.g. -run, -count, -short).
# TEST_PKG   — Go package pattern(s) to test (e.g. ./internal/..., ./cmd/...).
#
# Host tests (make test / test-verbose / test-race):
#   Run on the host. Tests that depend on external tools (ocrmypdf, gs, pdftotext)
#   will FAIL if those tools are not installed — install them to run the full suite.
#
# Container tests (make test-container):
#   Run inside an Arch Linux container with all external tools pre-installed.
#   Use this for CI or when you don't want to install tools on the host.
#
# Examples:
#   make test TEST_PKG="./internal/task"
#   make test-verbose TEST_PKG="./internal/task/..." TEST_FLAGS="-run TestEnqueue"
#   make test-race TEST_PKG="./internal/..." TEST_FLAGS="-count=1"
#   make test-container TEST_PKG="./internal/tools/adapters/..."

.PHONY: all build build-deps web-build clean run consume test test-race test-verbose test-container test-container-build build-musl-image build-musl

all: build

web-build:
	cd web && npm ci && npm run build
	rm -rf internal/static/build
	cp -r web/build internal/static/build

build-deps:
###	build lipng
	@if [ ! -d $(BUILD_DIR)/libpng ]; then \
		echo "Downloading libpng $(LIBNG_VER)..."; \
		mkdir -p $(BUILD_DIR); \
		curl -Ls https://download.sourceforge.net/libpng/libpng-$(LIBNG_VER).tar.gz | \
			tar xz -C $(BUILD_DIR); \
		mv $(BUILD_DIR)/libpng-$(LIBNG_VER) $(BUILD_DIR)/libpng; \
	fi
	cd $(BUILD_DIR)/libpng && rm -rf local/ && \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/libpng/local \
			--libdir=$(BUILD_DIR)/libpng/local/lib64 && \
		make -j$(shell nproc) && make install

###	build leptonica
	@if [ ! -d $(BUILD_DIR)/leptonica ]; then \
		echo "Cloning leptonica..."; \
		git clone https://github.com/DanBloomberg/leptonica.git $(BUILD_DIR)/leptonica; \
	fi
	cd $(BUILD_DIR)/leptonica && rm -rf local/ && ./autogen.sh && \
		CPPFLAGS="-I$(BUILD_DIR)/libpng/local/include" \
		LDFLAGS="-L$(BUILD_DIR)/libpng/local/lib64" \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/leptonica/local \
			--libdir=$(BUILD_DIR)/leptonica/local/lib64 \
			--without-libtiff --without-libwebp --without-libopenjpeg \
			--without-giflib --without-jpeg --disable-programs && \
		make -j$(shell nproc) && make install

###	build tesseract
	@if [ ! -d $(BUILD_DIR)/tesseract ]; then \
		echo "Cloning tesseract..."; \
		git clone https://github.com/tesseract-ocr/tesseract.git $(BUILD_DIR)/tesseract; \
	fi
	cd $(BUILD_DIR)/tesseract && rm -rf local/ && ./autogen.sh && \
		CPPFLAGS="-I$(BUILD_DIR)/libpng/local/include" \
		LDFLAGS="-L$(BUILD_DIR)/libpng/local/lib64" \
		PKG_CONFIG_PATH="$(BUILD_DIR)/leptonica/local/lib64/pkgconfig:$(BUILD_DIR)/libpng/local/lib64/pkgconfig" \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/tesseract/local \
			--with-extra-libraries=$(BUILD_DIR)/leptonica/local/lib64 \
			--with-extra-includes=$(BUILD_DIR)/leptonica/local/include \
			--with-curl=no --with-archive=no --disable-openmp --disable-legacy --disable-graphics && \
		make -j$(shell nproc) && make install

###	build mupdf
	@if [ ! -d $(MUPDF_DIR) ]; then \
		echo "Cloning MuPDF $(MUPDF_VER)..."; \
		git clone --depth 1 --branch $(MUPDF_VER) https://github.com/ArtifexSoftware/mupdf.git $(MUPDF_DIR); \
		cd $(MUPDF_DIR) && git submodule update --init --depth 1; \
	fi
	cd $(MUPDF_DIR) && rm -rf local/ && \
		make prefix=$(MUPDF_DIR)/local HAVE_X11=no HAVE_GLUT=no shared=no libs install

###	build tokenizers
	@if [ ! -f $(TOKENIZERS_DIR)/libtokenizers.a ]; then \
		echo "Cloning and building tokenizers from source for musl..."; \
		git clone --depth 1 https://github.com/daulet/tokenizers.git $(TOKENIZERS_DIR)/src; \
		cd $(TOKENIZERS_DIR)/src && \
			cargo build --release -p tokenizers-ffi; \
		cp $(TOKENIZERS_DIR)/src/target/release/libtokenizers_ffi.a $(TOKENIZERS_DIR)/libtokenizers.a; \
		echo "Built musl-compatible libtokenizers.a"; \
	fi

consume: build
	$(BINARY) consume

test:
	go test $(TEST_FLAGS) $(TEST_PKG)

test-race:
	go test -race $(TEST_FLAGS) $(TEST_PKG)

test-verbose:
	go test -v $(TEST_FLAGS) $(TEST_PKG)

test-container-build:
	podman build -t kushim-test -f Containerfile.test .

test-container: test-container-build
	podman run --rm \
		-v $(CURDIR):/app:Z \
		-w /app \
		-e CGO_ENABLED=1 \
		kushim-test \
		test $(TEST_FLAGS) $(TEST_PKG)

build-musl-image:
	podman build -t kushim-musl-builder -f Containerfile.musl .

_musl-build-deps:
	go mod download
	$(MAKE) build-deps

_musl-build:
	go build -tags "XLA,ORT" -o $(BINARY) ./cmd/kushim/main.go
	go build -tags "XLA,ORT" -o $(EDUB_BINARY) ./cmd/edub/main.go
	@echo "=== Done: static binaries at $(BINARY) and $(EDUB_BINARY) ==="

build-musl-deps: build-musl-image
	podman run --rm \
		-v $(CURDIR):/workspace:Z \
		-v $(HOME)/go/pkg/mod:/go/pkg/mod:Z \
		-w /workspace \
		--network host \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		kushim-musl-builder \
		make _musl-build-deps \
			BINARY=$(BINARY) \
			EDUB_BINARY=$(EDUB_BINARY)

build-musl: web-build
	podman run --rm \
		-v $(CURDIR):/workspace:Z \
		-v $(HOME)/go/pkg/mod:/go/pkg/mod:Z \
		-w /workspace \
		--network host \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		kushim-musl-builder \
		make _musl-build \
			BINARY=$(BINARY) \
			EDUB_BINARY=$(EDUB_BINARY)

clean:
	rm -f $(BINARY) $(EDUB_BINARY)
