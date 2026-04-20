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

# `make install` — system install into $(BIN_PREFIX) (default /usr/local/bin).
# This is the path `launchd`/`systemd` can actually exec, because on recent
# macOS versions binaries under ~/Documents are TCC-protected and hang dyld
# when launched outside an interactive shell. We also record the install
# method and, if a daemon service is already registered, reinstall it so
# it picks up the new binary path without requiring the user to remember.
.PHONY: install
install: build
	install -m 0755 $(BIN_DIR)/intent $(BIN_PREFIX)/intent
	ln -sf intent $(BIN_PREFIX)/i
	@$(BIN_PREFIX)/intent init record-install --method manual --channel $(CHANNEL) >/dev/null 2>&1 || true
	@if $(BIN_PREFIX)/intent daemon status >/dev/null 2>&1 \
	   || [ -f "$$HOME/Library/LaunchAgents/com.coreyrdean.intent.plist" ] \
	   || [ -f "$$HOME/.config/systemd/user/com.coreyrdean.intent.service" ]; then \
	  echo "reinstalling daemon service to pick up the new binary..." ; \
	  $(BIN_PREFIX)/intent daemon uninstall >/dev/null 2>&1 || true ; \
	  $(BIN_PREFIX)/intent daemon install ; \
	fi
	@echo "installed: $(BIN_PREFIX)/intent (and $(BIN_PREFIX)/i)"

.PHONY: uninstall
uninstall:
	-$(BIN_PREFIX)/intent daemon uninstall 2>/dev/null
	rm -f $(BIN_PREFIX)/intent $(BIN_PREFIX)/i

# `make link` — for local dev, symlinks ~/.local/bin/{intent,i} to the
# binaries we just built. Since ~/.local/bin is on PATH but lives under
# $HOME, this works without sudo and the symlinks always point at your
# latest `make build` so iterating doesn't require a copy step.
#
# Caveat (macOS): the launchd-managed `intentd` service cannot execute
# a binary whose real path is under ~/Documents — that directory is
# TCC-protected and launchd hangs in dyld before main() ever runs.
# For a working daemon you need `make install` (which puts the binary
# in /usr/local/bin) in addition to (or instead of) `make link`.
LINK_DIR ?= $(HOME)/.local/bin

.PHONY: link
link: build
	@mkdir -p $(LINK_DIR)
	@ln -sfn $(abspath $(BIN_DIR)/intent) $(LINK_DIR)/intent
	@ln -sfn $(abspath $(BIN_DIR)/i)      $(LINK_DIR)/i
	@echo "linked: $(LINK_DIR)/intent  -> $(abspath $(BIN_DIR)/intent)"
	@echo "linked: $(LINK_DIR)/i       -> $(abspath $(BIN_DIR)/i)"
	@$(LINK_DIR)/intent init record-install --method manual --channel dev >/dev/null 2>&1 || true

.PHONY: unlink
unlink:
	@rm -f $(LINK_DIR)/intent $(LINK_DIR)/i
	@echo "removed: $(LINK_DIR)/{intent,i}"

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
