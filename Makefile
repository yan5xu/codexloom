.PHONY: all web server cli build release run clean

all: build

# Build the React console into internal/webui/dist (embedded by Go).
web:
	cd web && npm install && npm run build

# Build CodexLoom binaries. Legacy names remain compatible entry points while
# existing launchd jobs and Agent scripts migrate.
build:
	go build -o bin/codex-loom ./cmd/codex-loom
	go build -o bin/codex-loom-reloader ./cmd/codex-loom-reloader
	go build -o bin/loom ./cmd/loom
	go build -o bin/loom-gateway ./cmd/loom-gateway
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
