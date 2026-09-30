GO := $(shell command -v go 2>/dev/null || printf '%s' .tools/go/bin/go)
export GOCACHE := $(CURDIR)/.tools/go-cache
VERSION := $(shell node -p "require('./package.json').version" 2>/dev/null || printf dev)
LDFLAGS := -X github.com/Aaravkhanal/GITOWN/internal/version.Version=$(VERSION)

.PHONY: dev test check build cli fmt
dev:
	npm run dev
test:
	$(GO) test -race ./...
check:
	$(GO) vet ./...
	npm run typecheck
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o .tools/gitown ./apps/server
	npm run build
cli:
	mkdir -p .tools/bin
	$(GO) build -ldflags "$(LDFLAGS)" -o .tools/bin/gitown ./apps/gitown
fmt:
	$(GO) fmt ./...
