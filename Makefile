.PHONY: build run test clean

# Build the binary
build:
	go build -o satsbook ./cmd/satsbook

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
