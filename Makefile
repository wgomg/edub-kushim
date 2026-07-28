CURDIR        ?= $(abspath .)
BUILD_DIR     := $(CURDIR)/build
BINARY        := ./dev/bin/kushim
EDUB_BINARY   := ./dev/bin/edub
TESS_INCLUDE  := $(BUILD_DIR)/tesseract/local/include
TESS_LIB      := $(BUILD_DIR)/tesseract/local/lib64
LEP_LIB       := $(BUILD_DIR)/leptonica/local/lib64
MUPDF_DIR     := $(BUILD_DIR)/mupdf
MUPDF_LIB     := $(MUPDF_DIR)/local/lib
LIBNG_VER     := 1.6.58
LIBNG_SHA256  := 8c9b05b675ca7301a458df2c2e46f26e1d41ff36b8863f8c33530bc58c2e6225
TESS_TAG      := 5.5.3
LEP_COMMIT    := 10bdea2f19240ffe8c931701b0b7340f41c6b256
MUPDF_VER     := 1.28.0
TOKENIZERS_DIR := $(BUILD_DIR)/tokenizers
KNOWN_NVM_DIRS := $(HOME)/.nvm $(HOME)/.config/nvm
NVM_DIR       := $(firstword $(wildcard $(KNOWN_NVM_DIRS)))

export CGO_ENABLED  := 1
export CGO_CPPFLAGS := -I$(TESS_INCLUDE) -I$(BUILD_DIR)/leptonica/local/include -I$(BUILD_DIR)/libpng/local/include
export CGO_LDFLAGS  := -L$(TOKENIZERS_DIR)

BUILD_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
LDFLAGS      := -s -w -X github.com/wgomg/edub-kushim/internal/version.Commit=$(BUILD_COMMIT) -X github.com/wgomg/edub-kushim/internal/version.Date=$(BUILD_DATE)

.PHONY: build build-deps web-build clean run consume build-musl-image build-musl build-mupdf-force compose-up compose-down

web-build:
	. "$(NVM_DIR)/nvm.sh" && nvm use && cd web && npm ci && npm audit fix || true && npm run build
	rm -rf internal/static/build
	cp -r web/build internal/static/build

wizard-build:
	. "$(NVM_DIR)/nvm.sh" && nvm use && cd web-wizard && npm ci && npm audit fix || true && npm run build
	rm -rf internal/wizard/static
	cp -r web-wizard/build internal/wizard/static

build:
	go build -tags "XLA,ORT" -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/kushim/main.go
	CGO_ENABLED=0 go build -tags "XLA,ORT" -ldflags="$(LDFLAGS)" -o $(EDUB_BINARY) ./cmd/edub

build-glibc:
	podman run --rm \
		-v $(CURDIR):/workspace:Z \
		-v $(HOME)/go/pkg/mod:/go/pkg/mod:Z \
		-w /workspace \
		--network host \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		kushim-glibc-builder \
		make build \
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
		make build \
			BINARY=$(BINARY) \
			EDUB_BINARY=$(EDUB_BINARY)

build-deps: build-libpng build-leptonica build-tesseract build-mupdf download-tokenizers

build-deps-musl: build-libpng build-leptonica build-tesseract build-mupdf build-tokenizers

build-musl-image:
	podman build -t kushim-musl-builder -f Containerfile.musl .

build-glibc-image:
	podman build -t kushim-glibc-builder -f Containerfile.glibc .

build-tools-image:
	podman build -t localhost/edub-kushim:latest -f Containerfile.full .

build-musl-deps:
	podman run --rm \
		-v $(CURDIR):/workspace:Z \
		-v $(HOME)/go/pkg/mod:/go/pkg/mod:Z \
		-w /workspace \
		--network host \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		kushim-musl-builder \
		make build-deps-musl \
			BINARY=$(BINARY) \
			EDUB_BINARY=$(EDUB_BINARY)

build-glibc-deps:
	podman run --rm \
		-v $(CURDIR):/workspace:Z \
		-v $(HOME)/go/pkg/mod:/go/pkg/mod:Z \
		-w /workspace \
		--network host \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		kushim-glibc-builder \
		make build-deps \
			BINARY=$(BINARY) \
			EDUB_BINARY=$(EDUB_BINARY)

build-libpng:
	@if [ ! -d $(BUILD_DIR)/libpng ]; then \
		echo "Downloading libpng $(LIBNG_VER)..."; \
		mkdir -p $(BUILD_DIR); \
		curl -Ls -o $(BUILD_DIR)/libpng-$(LIBNG_VER).tar.gz \
			https://download.sourceforge.net/libpng/libpng-$(LIBNG_VER).tar.gz; \
		echo "$(LIBNG_SHA256)  $(BUILD_DIR)/libpng-$(LIBNG_VER).tar.gz" | sha256sum -c - || exit 1; \
		tar xzf $(BUILD_DIR)/libpng-$(LIBNG_VER).tar.gz -C $(BUILD_DIR); \
		rm $(BUILD_DIR)/libpng-$(LIBNG_VER).tar.gz; \
		mv $(BUILD_DIR)/libpng-$(LIBNG_VER) $(BUILD_DIR)/libpng; \
	fi
	cd $(BUILD_DIR)/libpng && rm -rf local/ .libs/ libpng16.la && \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/libpng/local \
			--libdir=$(BUILD_DIR)/libpng/local/lib64 && \
		make -j$(shell nproc) && make install

build-leptonica:
	@if [ ! -d $(BUILD_DIR)/leptonica ]; then \
		echo "Cloning leptonica..."; \
		git clone https://github.com/DanBloomberg/leptonica.git $(BUILD_DIR)/leptonica; \
		cd $(BUILD_DIR)/leptonica && git checkout $(LEP_COMMIT); \
	fi
	cd $(BUILD_DIR)/leptonica && rm -rf local/ && ./autogen.sh && \
		CPPFLAGS="-I$(BUILD_DIR)/libpng/local/include" \
		LDFLAGS="-L$(BUILD_DIR)/libpng/local/lib64" \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/leptonica/local \
			--libdir=$(BUILD_DIR)/leptonica/local/lib64 \
			--without-libtiff --without-libwebp --without-libopenjpeg \
			--without-giflib --without-jpeg --disable-programs && \
		make -j$(shell nproc) && make install

build-tesseract:
	@if [ ! -d $(BUILD_DIR)/tesseract ]; then \
		echo "Cloning tesseract $(TESS_TAG)..."; \
		git clone --depth 1 --branch $(TESS_TAG) https://github.com/tesseract-ocr/tesseract.git $(BUILD_DIR)/tesseract; \
	fi
	cd $(BUILD_DIR)/tesseract && rm -rf local/ && make clean 2>/dev/null; ./autogen.sh && \
		CPPFLAGS="-I$(BUILD_DIR)/libpng/local/include" \
		LDFLAGS="-L$(BUILD_DIR)/libpng/local/lib64" \
		PKG_CONFIG_PATH="$(BUILD_DIR)/leptonica/local/lib64/pkgconfig:$(BUILD_DIR)/libpng/local/lib64/pkgconfig" \
		./configure --disable-shared --enable-static --prefix=$(BUILD_DIR)/tesseract/local \
			--libdir=$(BUILD_DIR)/tesseract/local/lib64 \
			--with-extra-libraries=$(BUILD_DIR)/leptonica/local/lib64 \
			--with-extra-includes=$(BUILD_DIR)/leptonica/local/include \
			--with-curl=no --with-archive=no --disable-openmp --disable-legacy --disable-graphics && \
		make -j$(shell nproc) && make install

build-mupdf-force:
	rm -rf $(MUPDF_DIR)
	$(MAKE) build-mupdf

build-mupdf:
	@if [ ! -d $(MUPDF_DIR) ]; then \
		echo "Cloning MuPDF $(MUPDF_VER)..."; \
		git clone --depth 1 --branch $(MUPDF_VER) https://github.com/ArtifexSoftware/mupdf.git $(MUPDF_DIR); \
		cd $(MUPDF_DIR) && git submodule update --init --depth 1; \
	fi
	cd $(MUPDF_DIR) && rm -rf local/ && \
		make prefix=$(MUPDF_DIR)/local HAVE_X11=no HAVE_GLUT=no shared=no libs install

download-tokenizers:
	@if [ ! -f $(TOKENIZERS_DIR)/libtokenizers.a ]; then \
		echo "Downloading libtokenizers.a..."; \
		mkdir -p $(TOKENIZERS_DIR); \
		curl -sL https://github.com/daulet/tokenizers/releases/latest/download/libtokenizers.linux-amd64.tar.gz \
			-o $(TOKENIZERS_DIR)/libtokenizers.tar.gz; \
		tar xzf $(TOKENIZERS_DIR)/libtokenizers.tar.gz -C $(TOKENIZERS_DIR); \
		rm $(TOKENIZERS_DIR)/libtokenizers.tar.gz; \
		echo "Downloaded libtokenizers.a to $(TOKENIZERS_DIR)"; \
	fi

build-tokenizers:
	@if [ ! -f $(TOKENIZERS_DIR)/libtokenizers.a ]; then \
		echo "Cloning and building tokenizers from source for musl..."; \
		git clone --depth 1 https://github.com/daulet/tokenizers.git $(TOKENIZERS_DIR)/src; \
		cd $(TOKENIZERS_DIR)/src && \
			cargo build --release -p tokenizers-ffi; \
		cp $(TOKENIZERS_DIR)/src/target/release/libtokenizers_ffi.a $(TOKENIZERS_DIR)/libtokenizers.a; \
		echo "Built musl-compatible libtokenizers.a"; \
	fi

compose-up:
	docker compose up --build

compose-down:
	docker compose down

fix:
	go fix -tags "XLA,ORT" ./...

# ── Testing ──────────────────────────────────────────────────────────
# test-short runs all tests that don't require CGo (Tesseract, MuPDF).
# These are safe for any environment and cover ~95% of the codebase.
.PHONY: test-short test test-verbose

test-short:
	CGO_ENABLED=0 go test -tags "XLA,ORT" -count=1 -timeout 60s \
		./internal/llm/ \
		./internal/config/ \
		./internal/api/types/ \
		./internal/tools/ \
		./internal/utils/ \
		./internal/tagmatch/ \
		./internal/storage/ \
		./internal/auth/ \
		./internal/api/ \
		./internal/tools/adapters/contentanalyzer/ \
		./internal/errs/ \
		./internal/pool/ \

# Default test target (same as test-short for now).
test: test-short

# Verbose output, useful during development.
test-verbose:
	CGO_ENABLED=0 go test -tags "XLA,ORT" -count=1 -v -timeout 60s \
		./internal/llm/ \
		./internal/config/ \
		./internal/api/types/ \
		./internal/tools/ \
		./internal/utils/ \
		./internal/tagmatch/ \
		./internal/storage/ \
		./internal/auth/ \
		./internal/api/ \
		./internal/tools/adapters/contentanalyzer/ \
		./internal/errs/ \
		./internal/pool/ \

# Database-dependent tests (requires PostgreSQL 16+).
.PHONY: test-db
test-db:
	CGO_ENABLED=0 go test -tags "XLA,ORT" -count=1 -timeout 120s \
		./internal/database/ \
		./internal/search/ \
		./internal/task/ \
		./internal/service/ \
		./internal/api/handlers/ \
		./internal/consumption/

# CGo-dependent tests (requires C toolchain + built C libraries on host,
# or run via test-cgo-glibc / test-cgo-musl in the container images).
.PHONY: test-cgo test-cgo-glibc test-cgo-musl

test-cgo:
	go test -tags "XLA,ORT" -count=1 -timeout 120s \
		./internal/tools/adapters \
		./internal/tools/adapters/ocr \
		./internal/tools/adapters/tagmatcher

test-cgo-glibc:
	podman run --rm \
		-v $(CURDIR):/workspace:Z \
		-v $(HOME)/go/pkg/mod:/go/pkg/mod:Z \
		-w /workspace \
		--network host \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		kushim-glibc-builder \
		make test-cgo

test-cgo-musl:
	podman run --rm \
		-v $(CURDIR):/workspace:Z \
		-v $(HOME)/go/pkg/mod:/go/pkg/mod:Z \
		-w /workspace \
		--network host \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 \
		kushim-musl-builder \
		make test-cgo
