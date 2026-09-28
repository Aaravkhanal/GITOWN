GO := $(shell command -v go 2>/dev/null || printf '%s' .tools/go/bin/go)
export GOCACHE := $(CURDIR)/.tools/go-cache

.PHONY: dev test check build fmt
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
fmt:
	$(GO) fmt ./...
