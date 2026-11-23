# Local gate for Colima UI. There is no hosted CI: `make check` is the gate.
# Written for GNU Make 3.81 (macOS default).

GO_PKGS := ./cmd/... ./internal/...

BIN := bin/colima-ui

.PHONY: check setup hooks check-web-deps build

# Runs every step in order and stops at the first failure.
check: check-web-deps
	@echo "==> gofmt"
	@out="$$(gofmt -l $$(git ls-files '*.go'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	@echo "==> go vet"
	go vet $(GO_PKGS)
	@echo "==> go test -race"
	go test -race $(GO_PKGS)
	@echo "==> web lint"
	cd web && npm run lint
	@echo "==> web test"
	cd web && npm run test
	@echo "==> web build"
	cd web && npm run build
	@echo "==> go build (CGO_ENABLED=0)"
	CGO_ENABLED=0 go build $(GO_PKGS)
	@echo "check: OK"

# Fails fast with a clear message; never installs anything.
check-web-deps:
	@if [ ! -d web/node_modules ]; then \
		echo "web/node_modules is missing. Run 'make setup' first." >&2; exit 1; fi

# Safe to run twice.
setup:
	cd web && npm ci
	$(MAKE) hooks

hooks:
	git config core.hooksPath .githooks

# One command from a clean checkout to one binary with the UI embedded.
build:
	cd web && npm ci && npm run build
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/colima-ui
	@echo "built $(BIN)"
