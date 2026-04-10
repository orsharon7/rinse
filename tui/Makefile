VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BINARY  := pr-review-tui
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install uninstall cross clean

## build: compile the binary for the current platform
build:
	go build $(LDFLAGS) -o $(BINARY) .

## install: build and install to INSTALL_DIR (default: ~/.local/bin)
install: build
	@install -d "$(or $(INSTALL_DIR),$(HOME)/.local/bin)"
	install -m 755 $(BINARY) "$(or $(INSTALL_DIR),$(HOME)/.local/bin)/$(BINARY)"
	@echo "Installed → $(or $(INSTALL_DIR),$(HOME)/.local/bin)/$(BINARY)"

## uninstall: remove the installed binary
uninstall:
	rm -f "$(or $(INSTALL_DIR),$(HOME)/.local/bin)/$(BINARY)"
	@echo "Removed → $(or $(INSTALL_DIR),$(HOME)/.local/bin)/$(BINARY)"

## cross: build binaries for all supported platforms into dist/
cross:
	@mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 .
	GOOS=linux   GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-arm64 .
	GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 .
	@echo "Built cross-platform binaries in dist/"

## clean: remove build artifacts
clean:
	rm -f $(BINARY)
	rm -rf dist/
