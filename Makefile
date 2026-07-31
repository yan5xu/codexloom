.PHONY: all web binaries verify-embedded-web build release run clean

VERSION ?= 0.1.0-dev
COMMIT := $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)$(shell test -z "$$(git status --porcelain 2>/dev/null)" || printf -- -dirty)
BUILT_AT := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/yan5xu/codex-loom/internal/buildinfo.Version=$(VERSION) -X github.com/yan5xu/codex-loom/internal/buildinfo.Commit=$(COMMIT) -X github.com/yan5xu/codex-loom/internal/buildinfo.BuiltAt=$(BUILT_AT)
GO_BUILD := go build -ldflags "$(LDFLAGS)"

all: build

# Build the React console into internal/webui/dist (embedded by Go).
web:
	cd web && npm ci && npm run build

# Build CodexLoom binaries only after refreshing the WebUI. The WebUI is
# embedded by Go at compile time, so reversing this dependency publishes a
# binary that keeps serving the previous frontend after restart.
binaries: web
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

# Fail the build if the compiled server does not contain the current Vite
# entrypoint. This catches stale or manually reordered embed builds.
verify-embedded-web: binaries
	@asset=$$(sed -n 's/.*src="\/\([^"?]*\.js\)".*/\1/p' internal/webui/dist/index.html | head -1); \
		test -n "$$asset" || { echo "cannot identify WebUI entrypoint" >&2; exit 1; }; \
		strings bin/codex-loom | grep -F "$$asset" >/dev/null || { echo "bin/codex-loom does not embed $$asset" >&2; exit 1; }; \
		echo "verified embedded WebUI: $$asset"

# Canonical production build. Do not replace this with a bare go build: Go
# embeds whatever was already present in internal/webui/dist.
build: verify-embedded-web

# Compatibility alias retained for existing operator instructions.
release: build

run: build
	./bin/codex-loom

clean:
	rm -rf bin internal/webui/dist/* web/node_modules
	touch internal/webui/dist/.gitkeep
