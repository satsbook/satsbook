.PHONY: build build-lndcheck run run-binary run-local run-umbrel test clean docker docker-multiarch

# Load environment variables from .env file if it exists
ifneq (,$(wildcard .env))
	include .env
	export
endif

# Build the binary
build:
	go build -o satsbook ./cmd/satsbook

# Build the lndcheck utility
build-lndcheck:
	go build -o lndcheck ./cmd/lndcheck

# Run the application (via go run, works in any shell)
run:
	go run ./cmd/satsbook

# Run the built binary (works in any shell via Make's env export)
run-binary: build
	./satsbook

# Run the built binary via bash (explicit env sourcing, works from fish/zsh/bash)
run-local: build
	bash -c 'set -a && source .env && ./satsbook'

# Run as Umbrel app (uses Umbrel-injected env vars, no .env file)
run-umbrel: build
	./satsbook

# Run tests
test:
	go test -v ./...

# Build Docker image (local platform)
docker:
	docker build -t satsbook:latest .

# Build multi-arch Docker image (amd64 + arm64 for Umbrel/Raspberry Pi)
docker-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/satsbook/satsbook:latest .

# Clean build artifacts
clean:
	rm -f satsbook lndcheck
	go clean
