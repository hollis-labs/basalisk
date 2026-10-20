.PHONY: all build run install ui-build ui-dev test vet clean

# Build the frontend then the Go binary — a production binary with the
# dashboard UI embedded.
all: ui-build build

# Build the dashboard UI into internal/webui/dist (the Go embed directory).
ui-build:
	cd frontend && npm ci && npm run build

# Run the dashboard UI dev server with hot reload. Proxies /api to the Go
# server on 127.0.0.1:8080 — run `make run` in another shell. The dev UI is served
# under / (see `base` in frontend/vite.config.ts).
ui-dev:
	cd frontend && npm ci && npm run dev

# Build the Go binary. Embeds whatever is in internal/webui/dist; run
# `make ui-build` first for a binary that serves the real UI.
build:
	go build -o basalisk ./cmd/basalisk

# Build and run the server.
run: build
	./basalisk

# Build the embedded UI and install the binary to $GOBIN — a self-contained
# basalisk on PATH that serves the real dashboard UI.
install: ui-build
	go install ./cmd/basalisk

test:
	go test -race ./cmd/... ./internal/...

vet:
	go vet ./cmd/... ./internal/...

# Remove build artifacts. Keeps internal/webui/dist/.gitkeep so the
# //go:embed directive still compiles.
clean:
	rm -f basalisk
	rm -rf frontend/node_modules
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
