# satsbook

**Bitcoin node analytics and accounting tool for sovereign Lightning operators.**

Track routing fees, manage cost basis, and generate tax reports—all on your own hardware.

<img width="512" alt="Satsbook" src="https://github.com/user-attachments/assets/a3af6c3a-58d0-4ab7-9bf1-d37c5f805a74" />

## What is Satsbook?

Satsbook is a privacy-first Lightning node analytics and accounting application designed for operators running Bitcoin infrastructure on their own hardware. It provides real-time insights into routing fees, cost basis tracking for tax purposes, and reporting tools—without requiring cloud services or compromising node privacy.

### Key Features

- **Fee Analytics**: Real-time monitoring of routing income and channel statistics
- **Cost Basis Tracking**: Automated import from Strike and other exchanges
- **Tax Reports**: Generate year-end reports for tax filing (Pro features)
- **Privacy-First**: Runs entirely on your hardware, no cloud dependencies
- **Self-Hosted**: Deploy to Umbrel or run standalone
- **No CGO**: Pure Go with ARM64 support for Raspberry Pi

## Quick Start

### Requirements

- **Go 1.23+** (or use the pre-built binary)
- **LND 0.17+** running locally or on your network
- **SQLite** (bundled—no external database needed)

### Installation

#### On Umbrel
_Satsbook is in development. Umbrel app store support coming in Phase 3._

#### Manual Setup

```bash
# Clone the repository
git clone https://github.com/satsbook/satsbook.git
cd satsbook

# Build the binary
make build

# Run the application
make run
```

The application will start on `http://localhost:8080` by default.

### Configuration

Satsbook uses environment variables for configuration:

```bash
# LND Connection (required)
LND_IP=localhost
LND_GRPC_PORT=10009
LND_MACAROON_PATH=/path/to/admin.macaroon
LND_TLS_CERT_PATH=/path/to/tls.cert

# Application Server
SATSBOOK_LISTEN_ADDR=:8080
SATSBOOK_DATA_DIR=./data
```

See `.env.example` for all available options.

## Development

### Project Structure

```
cmd/
  satsbook/          # Main application entry point
  lndcheck/          # LND connection diagnostics tool
internal/
  lnd/               # LND gRPC client wrapper
  db/                # SQLite database layer
  config/            # Configuration management
  exchange/          # CSV parsers (Strike, River, Swan)
  tax/               # Cost basis and taxable event logic
  web/               # HTTP handlers and templates
```

### Building & Testing

```bash
# Build the binary
make build

# Run tests
make test

# Run the application locally
make run

# Clean build artifacts
make clean
```

### Smoke Test (requires LND node)

Run the smoke test against a real LND node to verify the full data pipeline.
The script must be run from the repo root:

```bash
# With a .env file configured (see .env.example)
./scripts/smoke-test.sh

# Or pass connection details directly
./scripts/smoke-test.sh \
  --macaroon ~/.lnd/data/chain/bitcoin/mainnet/readonly.macaroon \
  --tls-cert ~/.lnd/tls.cert

# With a prebuilt binary (no Go toolchain needed on the server)
GOOS=linux GOARCH=amd64 go build -o satsbook ./cmd/satsbook
scp satsbook scripts/smoke-test.sh yourserver:~/project/
ssh yourserver 'cd ~/project && ./scripts/smoke-test.sh --binary ./satsbook'
```

Run `./scripts/smoke-test.sh --help` for all options.

## Architecture Highlights

- **Single Binary**: Compiled Go binary with no runtime dependencies
- **No CGO**: Uses `modernc.org/sqlite` for cross-platform ARM64 support
- **Embedded Assets**: All UI and templates bundled in the binary
- **Incremental Syncs**: Uses `sync_state` table to track processed data
- **Clean Architecture**: Clear separation between LND integration, database, and web layers

## Contributing

This project is under active development. To contribute:

1. Check the [GitHub project board](https://github.com/orgs/satsbook/projects/1)
2. Review open issues and discuss before starting work
3. Ensure tests pass: `make test`
4. Commit with conventional commits: `feat:`, `fix:`, `refactor:`, etc.

## Support

- **Bug Reports**: [GitHub Issues](https://github.com/satsbook/satsbook/issues)
- **LND Setup Help**: Check the [lightning-node-tools](https://github.com/satsbook/lightning-node-tools) repository

## License

**Core features** (Phase 0-1): [MIT License](./LICENSE) — free and open-source

**Pro features** (Phase 2+): Proprietary — tax export, advanced imports, and monetized features are closed-source

This dual-licensing model allows us to keep the foundation open while supporting development of premium features.
