BINARY  := revimg
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"

.PHONY: all build frontend backend clean run dev tidy

# ── Default ────────────────────────────────────────────────────────────────────
all: build

# ── Full production build ──────────────────────────────────────────────────────
build: frontend backend

# ── Frontend ───────────────────────────────────────────────────────────────────
frontend:
	@echo "→ Building frontend…"
	cd frontend && bun install --silent && bun run build
	@echo "✓ Frontend built → frontend/dist"

# ── Backend ────────────────────────────────────────────────────────────────────
backend:
	@echo "→ Building Go binary…"
	go build $(LDFLAGS) -o $(BINARY) .
	@echo "✓ Binary: ./$(BINARY)"

# ── Dev server (Go backend + Vite proxy) ───────────────────────────────────────
dev:
	@echo "→ Starting dev mode (run 'make dev-ui' in another terminal)"
	go run . &
	cd frontend && bun run dev

dev-ui:
	cd frontend && bun run dev

# ── Run production binary ──────────────────────────────────────────────────────
run: build
	./$(BINARY)

# ── Module tidy ────────────────────────────────────────────────────────────────
tidy:
	go mod tidy

# ── Clean ──────────────────────────────────────────────────────────────────────
clean:
	rm -f $(BINARY)
	rm -rf frontend/dist frontend/node_modules frontend/bun.lockb

# ── Cross-compile targets ──────────────────────────────────────────────────────
build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 .

build-darwin:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-darwin-arm64 .

build-windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-windows-amd64.exe .

build-all: frontend build-linux build-darwin build-windows
