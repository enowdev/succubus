BIN      := bin/succubus
PKG      := ./cmd/succubus
WEB      := web
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X github.com/enowdev/succubus/internal/mode.Version=$(VERSION)

.DEFAULT_GOAL := help

## help: list the targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

## build: build the SPA and compile the single binary
build: web-build
	go build -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)
	@echo "built $(BIN) ($(VERSION)) with the dashboard embedded"

## build-go: compile without rebuilding the SPA
build-go:
	go build -o $(BIN) $(PKG)

## web-build: produce web/dist for embedding
web-build:
	cd $(WEB) && bun install --frozen-lockfile 2>/dev/null || (cd $(WEB) && bun install)
	cd $(WEB) && bun run build

## dev: run the daemon and the Vite dev server together
dev:
	@echo "daemon  → http://127.0.0.1:7801"
	@echo "web     → http://localhost:5273"
	@$(MAKE) -j2 dev-daemon dev-web

dev-daemon: build-go
	$(BIN) daemon --dev

dev-web:
	cd $(WEB) && bun run dev

## test: run the Go test suite
test:
	go test ./... -count=1

## test-race: run the tests under the race detector
test-race:
	go test ./... -race -count=1

## check: vet, typecheck, test, and confirm every platform still builds
check: test
	go vet ./...
	GOOS=windows go vet ./...
	cd $(WEB) && bunx tsc --noEmit
	@$(MAKE) --no-print-directory cross

## install: build and copy the binary onto your PATH
install: build
	@dest=$${GOBIN:-$${HOME}/.local/bin}; \
	mkdir -p "$$dest"; \
	install -m 0755 $(BIN) "$$dest/succubus"; \
	echo "installed to $$dest/succubus"; \
	case ":$$PATH:" in \
	  *":$$dest:"*) ;; \
	  *) echo ""; \
	     echo "  ! $$dest is not on your PATH, so \`succubus\` will not be found."; \
	     echo "    Add this to ~/.zshrc (or ~/.bashrc) and restart your shell:"; \
	     echo ""; \
	     echo "        export PATH=\"$$dest:\$$PATH\""; \
	     echo "";; \
	esac

## seed: fill a running daemon with a minimal project
seed:
	./scripts/seed.sh

## demo: fill a running daemon with realistic multi-agent data
demo:
	./scripts/demo.sh

## screenshots: regenerate docs/screenshots from a running dev server
screenshots: demo
	node scripts/screenshot.mjs

## release: cross-compile every supported platform into dist/, with checksums
release: web-build
	@rm -rf dist && mkdir -p dist
	@for target in \
		darwin/amd64 darwin/arm64 \
		linux/amd64 linux/arm64 \
		windows/amd64 windows/arm64 \
		freebsd/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="dist/succubus-$(VERSION)-$$os-$$arch$$ext"; \
		echo "  $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -ldflags '$(LDFLAGS)' -o "$$out" $(PKG) || exit 1; \
	done
	@$(MAKE) --no-print-directory checksums
	@echo "built $(VERSION) for 7 platforms"

## checksums: write dist/checksums.txt (the installers verify against this)
checksums:
	@cd dist && rm -f checksums.txt && \
	if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum succubus-* > checksums.txt; \
	else \
		shasum -a 256 succubus-* > checksums.txt; \
	fi
	@echo "  dist/checksums.txt"

## cross: verify every platform still compiles, without producing binaries
cross:
	@for target in \
		darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 \
		windows/amd64 windows/arm64 freebsd/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o /dev/null $(PKG) \
			&& echo "  ok   $$target" || { echo "  FAIL $$target"; exit 1; }; \
	done

## clean: remove build output
clean:
	rm -rf $(BIN) dist $(WEB)/dist
	git checkout -- $(WEB)/dist 2>/dev/null || true

.PHONY: help build build-go web-build dev dev-daemon dev-web test test-race \
        check install seed demo screenshots release cross clean
