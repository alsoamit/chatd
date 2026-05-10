.PHONY: build test vet tidy install run clean release release-linux-amd64 release-linux-arm64 release-darwin-amd64 release-darwin-arm64 checksums

PREFIX  ?= $(HOME)/.local
BIN_DIR := $(PREFIX)/bin

BUILD_DIR := bin
DIST_DIR  := dist

# Pin a deterministic version string. CI overrides VERSION with the tag.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

VERSION_PKG := github.com/cedrx/chatd/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT)

GO_BUILD := CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)'

build:
	mkdir -p $(BUILD_DIR)
	$(GO_BUILD) -o $(BUILD_DIR)/chatd       ./cmd/daemon
	$(GO_BUILD) -o $(BUILD_DIR)/chat        ./cmd/chat
	$(GO_BUILD) -o $(BUILD_DIR)/chat-client ./cmd/chat-client

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

install: build
	install -d $(BIN_DIR)
	install -m 0755 $(BUILD_DIR)/chatd       $(BIN_DIR)/chatd
	install -m 0755 $(BUILD_DIR)/chat        $(BIN_DIR)/chat
	install -m 0755 $(BUILD_DIR)/chat-client $(BIN_DIR)/chat-client

run: build
	./$(BUILD_DIR)/chatd

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)

# --- Release tarballs --------------------------------------------------
#
# `make release` produces distributable tarballs for the two Linux
# architectures we ship: amd64 and arm64. CGO is disabled, binaries are
# statically linked, three binaries plus the systemd unit, env example,
# and install script are bundled per tarball.

release: clean release-linux-amd64 release-linux-arm64 release-darwin-amd64 release-darwin-arm64 checksums
	@echo "release artefacts in $(DIST_DIR)/"
	@ls -la $(DIST_DIR)

release-linux-amd64:
	$(MAKE) -s _build_arch GOOS=linux GOARCH=amd64

release-linux-arm64:
	$(MAKE) -s _build_arch GOOS=linux GOARCH=arm64

release-darwin-amd64:
	$(MAKE) -s _build_arch GOOS=darwin GOARCH=amd64

release-darwin-arm64:
	$(MAKE) -s _build_arch GOOS=darwin GOARCH=arm64

# Internal target: build + tar one (GOOS, GOARCH).
.PHONY: _build_arch
_build_arch:
	@mkdir -p $(DIST_DIR)
	@stage=$$(mktemp -d) ; \
	  out=chatd-$(VERSION)-$(GOOS)-$(GOARCH) ; \
	  mkdir -p $$stage/$$out ; \
	  echo ">> building $$out" ; \
	  CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $$stage/$$out/chatd ./cmd/daemon ; \
	  CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $$stage/$$out/chat ./cmd/chat ; \
	  CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $$stage/$$out/chat-client ./cmd/chat-client ; \
	  cp README.md .env.example $$stage/$$out/ ; \
	  cp DEPLOY.md $$stage/$$out/ 2>/dev/null || true ; \
	  cp systemd/chatd.service $$stage/$$out/chatd.service 2>/dev/null || true ; \
	  cp launchd/io.chatd.plist $$stage/$$out/io.chatd.plist 2>/dev/null || true ; \
	  cp scripts/install.sh $$stage/$$out/install.sh 2>/dev/null || true ; \
	  cp scripts/uninstall.sh $$stage/$$out/uninstall.sh 2>/dev/null || true ; \
	  tar -C $$stage -czf $(DIST_DIR)/$$out.tar.gz $$out ; \
	  rm -rf $$stage ; \
	  echo "   -> $(DIST_DIR)/$$out.tar.gz"

checksums:
	@cd $(DIST_DIR) && sha256sum *.tar.gz > SHA256SUMS && cat SHA256SUMS
