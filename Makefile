.PHONY: build build-lndcheck run test clean

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

# Run the application
run:
	go run ./cmd/satsbook

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -f satsbook lndcheck
	go clean
