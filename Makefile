.PHONY: all build run install ui-build ui-dev test vet lint pins scan generate clean

SUBSTRATE := go run github.com/hollis-labs/substrate/cmd/substrate

# Build the frontend then the Go binary: a production binary with the
# dashboard UI embedded.
all: ui-build build

# Build the dashboard UI into internal/webui/dist (the Go embed directory).
ui-build:
	cd frontend && npm ci && npm run build

# Run the dashboard UI dev server with hot reload. It proxies /api to the Go
# server (default 127.0.0.1:8085): run `make run` in another shell.
ui-dev:
	cd frontend && npm ci && npm run dev

# Build the Go binary. Embeds whatever is in internal/webui/dist; run
# `make ui-build` first for a binary that serves the real UI.
build:
	go build -o basalisk ./cmd/basalisk

# Build and run the server. Data lives under $XDG_DATA_HOME/basalisk, or
# BASALISK_DATA_DIR when set; see docs/architecture.md.
run: build
	./basalisk

# Build the embedded UI and install the binary to $GOBIN: a self-contained
# basalisk on PATH that serves the real dashboard UI.
install: ui-build
	go install ./cmd/basalisk

test:
	go test -race ./cmd/... ./internal/...

vet:
	go vet ./cmd/... ./internal/...

lint:
	golangci-lint run

# The go.mod pins must match the Substrate BOM; edit the BOM (in Substrate), never go.mod.
pins:
	$(SUBSTRATE) pins check --app .

scan:
	python3 scripts/scan-public --ref HEAD

# Regenerate the wiring file after editing cmd/basalisk/substrate.yaml.
generate:
	$(SUBSTRATE) generate -f cmd/basalisk/substrate.yaml

# Remove build artifacts. Keeps internal/webui/dist/.gitkeep so the
# //go:embed directive still compiles.
clean:
	rm -f basalisk
	rm -rf frontend/node_modules
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
