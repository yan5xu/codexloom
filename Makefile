.PHONY: all web server cli build run clean

all: build

# Build the React console into internal/webui/dist (embedded by Go).
web:
	cd web && npm install && npm run build

# Build both binaries (embeds whatever is currently in internal/webui/dist).
build:
	go build -o bin/codex-hub ./cmd/codex-hub
	go build -o bin/chub ./cmd/chub

# Full build: web console + binaries.
release: web build

run: build
	./bin/codex-hub

clean:
	rm -rf bin internal/webui/dist/* web/node_modules
	touch internal/webui/dist/.gitkeep
