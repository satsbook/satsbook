.PHONY: build build-lndcheck build-licenseserver run run-binary run-local run-umbrel run-daemon run-demo run-licenseserver stop stop-demo stop-licenseserver test clean docker docker-multiarch

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

# Run in the background, survives terminal exit (logs → satsbook.log, PID → satsbook.pid)
run-daemon: build
	@bash -c 'set -a && source .env && nohup ./satsbook > satsbook.log 2>&1 & echo $$! > satsbook.pid'
	@echo "Started (PID $$(cat satsbook.pid)). Logs: satsbook.log"

# Run the demo license validation server in the background (requires SATSBOOK_LICENSE_SIGNING_KEY in .env)
run-demo:
	@go build -o demoserver ./cmd/demoserver
	@bash -c 'set -a && source .env && nohup ./demoserver real 3098 > demoserver.log 2>&1 & echo $$! > demoserver.pid'
	@echo "Demo validation server started (PID $$(cat demoserver.pid), port 3098). Logs: demoserver.log"

# Stop the daemonized process (PID file + fallback to port scan)
stop:
	@if [ -f satsbook.pid ]; then \
		kill $$(cat satsbook.pid) 2>/dev/null; rm -f satsbook.pid; \
	fi
	@PID=$$(lsof -ti :$${SATSBOOK_APP_PORT:-3000} 2>/dev/null | head -1); \
	if [ -n "$$PID" ]; then \
		kill $$PID 2>/dev/null; sleep 0.5; \
		if kill -0 $$PID 2>/dev/null; then kill -9 $$PID 2>/dev/null; fi; \
	fi
	@rm -f satsbook.pid
	@echo "Stopped satsbook."

# Stop the demo validation server
stop-demo:
	@if [ -f demoserver.pid ]; then \
		kill $$(cat demoserver.pid) 2>/dev/null; rm -f demoserver.pid; \
	fi
	@PID=$$(lsof -ti :3098 2>/dev/null | head -1); \
	if [ -n "$$PID" ]; then \
		kill $$PID 2>/dev/null; sleep 0.5; \
		if kill -0 $$PID 2>/dev/null; then kill -9 $$PID 2>/dev/null; fi; \
	fi
	@rm -f demoserver.pid
	@echo "Stopped demo server."

# Build the license server
build-licenseserver:
	go build -o licenseserver ./cmd/licenseserver

# Run the license server in the background
run-licenseserver: build-licenseserver
	@bash -c 'set -a && source .env && nohup ./licenseserver serve > licenseserver.log 2>&1 & echo $$! > licenseserver.pid'
	@echo "License server started (PID $$(cat licenseserver.pid), port $${PORT:-8080}). Logs: licenseserver.log"

# Stop the license server
stop-licenseserver:
	@if [ -f licenseserver.pid ]; then \
		kill $$(cat licenseserver.pid) 2>/dev/null; rm licenseserver.pid; echo "Stopped license server."; \
	else \
		echo "No licenseserver.pid found — is it running?"; \
	fi

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
	rm -f satsbook lndcheck demoserver licenseserver
	go clean
