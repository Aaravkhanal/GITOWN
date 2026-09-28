GO := $(shell command -v go 2>/dev/null || printf '%s' .tools/go/bin/go)
export GOCACHE := $(CURDIR)/.tools/go-cache

.PHONY: dev test check build cli fmt
dev:
	npm run dev
test:
	$(GO) test -race ./...
check:
	$(GO) vet ./...
	npm run typecheck
build:
	$(GO) build -o .tools/gitown ./apps/server
	npm run build
cli:
	mkdir -p .tools/bin
	$(GO) build -o .tools/bin/gitown ./apps/gitown
fmt:
	$(GO) fmt ./...
