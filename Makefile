# intent — developer Makefile.
#
# `make`         — build for the host (`./bin/intent` and `./bin/i`).
# `make test`    — run all unit tests.
# `make check`   — vet + test.
# `make install` — install ./bin/intent to /usr/local/bin (and `i` symlink).
# `make clean`   — remove ./bin and ./dist.
# `make release` — build the cross-platform matrix into ./dist (used by CI).

GO          ?= go
PKG          := github.com/CoreyRDean/intent
BIN_DIR      := ./bin
DIST_DIR     := ./dist
PREFIX       ?= /usr/local
BIN_PREFIX   := $(PREFIX)/bin

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILDDATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
CHANNEL   ?= dev

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.BuildDate=$(BUILDDATE) \
	-X $(PKG)/internal/version.Channel=$(CHANNEL)

.PHONY: all
all: build

.PHONY: build
build: $(BIN_DIR)/intent $(BIN_DIR)/i

$(BIN_DIR)/intent: $(shell find . -name '*.go' -not -path './dist/*' 2>/dev/null)
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $(BIN_DIR)/intent ./cmd/intent

$(BIN_DIR)/i: $(BIN_DIR)/intent
	@ln -sf intent $(BIN_DIR)/i

.PHONY: test
test:
	$(GO) test ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: check
check: vet test

.PHONY: install
install: build
	install -m 0755 $(BIN_DIR)/intent $(BIN_PREFIX)/intent
	ln -sf intent $(BIN_PREFIX)/i

.PHONY: uninstall
uninstall:
	rm -f $(BIN_PREFIX)/intent $(BIN_PREFIX)/i

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)

# Release builds for all supported targets. Output: dist/intent-<os>-<arch>[.exe]
RELEASE_TARGETS := \
	linux-amd64 \
	linux-arm64 \
	darwin-amd64 \
	darwin-arm64

.PHONY: release
release: $(addprefix $(DIST_DIR)/intent-,$(RELEASE_TARGETS))

$(DIST_DIR)/intent-linux-amd64:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $@ ./cmd/intent

$(DIST_DIR)/intent-linux-arm64:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $@ ./cmd/intent

$(DIST_DIR)/intent-darwin-amd64:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $@ ./cmd/intent

$(DIST_DIR)/intent-darwin-arm64:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $@ ./cmd/intent

.PHONY: checksums
checksums: release
	cd $(DIST_DIR) && shasum -a 256 intent-* > SHA256SUMS
