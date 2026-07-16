.PHONY: all web server cli build release run clean

VERSION ?= 0.1.0-dev
COMMIT := $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)$(shell test -z "$$(git status --porcelain 2>/dev/null)" || printf -- -dirty)
BUILT_AT := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/yan5xu/codex-loom/internal/buildinfo.Version=$(VERSION) -X github.com/yan5xu/codex-loom/internal/buildinfo.Commit=$(COMMIT) -X github.com/yan5xu/codex-loom/internal/buildinfo.BuiltAt=$(BUILT_AT)
GO_BUILD := go build -ldflags "$(LDFLAGS)"

all: build

# Build the React console into internal/webui/dist (embedded by Go).
web:
	cd web && npm install && npm run build

# Build CodexLoom binaries. Legacy names remain compatible entry points while
# existing launchd jobs and Agent scripts migrate.
build:
	$(GO_BUILD) -o bin/codex-loom ./cmd/codex-loom
	$(GO_BUILD) -o bin/codex-loom-reloader ./cmd/codex-loom-reloader
	$(GO_BUILD) -o bin/loom ./cmd/loom
	$(GO_BUILD) -o bin/loom-gateway ./cmd/loom-gateway
	$(GO_BUILD) -o bin/loom-feishu-gateway ./cmd/loom-feishu-gateway
	$(GO_BUILD) -o bin/loom-slack-gateway ./cmd/loom-slack-gateway
	$(GO_BUILD) -o bin/loom-parall-gateway ./cmd/loom-parall-gateway
	cp bin/codex-loom bin/codex-hub
	cp bin/codex-loom-reloader bin/codex-hub-reloader
	cp bin/loom bin/chub
	cp bin/loom-gateway bin/chub-gateway

# Full build: web console + binaries.
release: web build

run: build
	./bin/codex-loom

clean:
	rm -rf bin internal/webui/dist/* web/node_modules
	touch internal/webui/dist/.gitkeep
