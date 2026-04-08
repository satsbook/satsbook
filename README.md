# Satsbook

**A general ledger for your Lightning node.**

Satsbook turns your self-hosted Bitcoin node into a tiny business you can actually account for: routing income, exchange purchases, cost basis, and a unified net position — all running on your own hardware, no cloud, no data leaving your node.

<img width="512" alt="Satsbook" src="https://github.com/user-attachments/assets/a3af6c3a-58d0-4ab7-9bf1-d37c5f805a74" />

---

## Why Satsbook exists

If you run your own Lightning node, you already know the gap:

- **ThunderHub / RTL** show you routing events, but not *what you earned this year*.
- **Koinly / CoinLedger** do crypto taxes, but don't know Lightning exists.
- **Amboss** targets professional routing operators, not home-lab sovereigns.
- **Your brokerage's CSV** tells you half the story. Your node tells you the other half. Nothing stitches them together.

Satsbook is the missing piece for prosumer Bitcoin operators who take custody seriously and still want to know what their stack is actually doing financially.

## What you get (free tier)

- **Net BTC position** across your LND wallet, Strike, River, and Coinbase — one number, one glance
- **Routing fee income** — 7d / 30d / YTD / all-time with per-channel breakdown
- **CSV imports** for Strike, River, and Coinbase (BTC transactions only, de-duped by content hash)
- **Basic P&L** — purchased vs. received vs. sold, YTD cost basis
- **Runs entirely on your node** — no accounts, no telemetry, no outbound calls except a single CoinGecko price lookup
- **One binary, ~27 MB, ARM64 + amd64** — works on a Raspberry Pi

Coming in Pro ($9/mo) and Power ($19/mo) tiers: FIFO/LIFO cost basis, Form 8949 export, on-chain cost basis, Monarch/YNAB sync, Telegram alerts, multi-node. See [pricing](#pricing) below.

## Install

### On Umbrel (recommended)

```
Umbrel → App Store → Community App Stores → Add Store
https://github.com/satsbook/umbrel-app-store
```

Then install **Satsbook** from the Community tab. It'll auto-detect your Lightning node and start ingesting routing events on the next sync cycle.

### Docker

```bash
docker run -d \
  --name satsbook \
  -p 3000:3000 \
  -v satsbook-data:/data \
  -e LND_IP=<your-lnd-host> \
  -e LND_GRPC_PORT=10009 \
  -e LND_MACAROON_PATH=/lnd/readonly.macaroon \
  -e LND_TLS_CERT_PATH=/lnd/tls.cert \
  -v /path/to/lnd:/lnd:ro \
  ghcr.io/satsbook/satsbook:latest
```

### From source

```bash
git clone https://github.com/satsbook/satsbook.git
cd satsbook
cp .env.example .env   # fill in LND paths
make build
make run
```

Open http://localhost:3000.

A **read-only** LND macaroon is sufficient — Satsbook never signs, sends, or modifies anything on your node.

## Screenshots

_Add once v1.0 ships — dashboard headline, YTD strip, onboarding flow, P&L page, import page with danger zone._

## Pricing

| | Free | Pro ($9/mo) | Power ($19/mo) |
|---|---|---|---|
| Routing fee dashboard | ✓ | ✓ | ✓ |
| Exchange CSV imports (Strike, River, Coinbase) | ✓ | ✓ | ✓ |
| Net BTC position | ✓ | ✓ | ✓ |
| Basic P&L | ✓ | ✓ | ✓ |
| Full history export | — | ✓ | ✓ |
| FIFO / LIFO cost basis | — | ✓ | ✓ |
| Form 8949 tax export | — | ✓ | ✓ |
| On-chain cost basis | — | ✓ | ✓ |
| Monarch / YNAB sync | — | — | ✓ |
| Telegram alerts | — | — | ✓ |
| Multi-node support | — | — | ✓ |
| Lightning-native checkout | — | — | ✓ |

Pro/Power tiers are under development. The free tier is genuinely useful on its own — it's not a demo.

## Architecture

- **One Go binary**, no CGO, ~27 MB — cross-compiles cleanly to `linux/arm64` for Raspberry Pi
- **SQLite** via `modernc.org/sqlite` (pure Go) — no external database
- **HTMX** frontend — server-rendered HTML with embedded templates, no JS framework
- **Embedded assets** (`embed.FS`) — everything ships in the binary
- **Incremental syncs** via a `sync_state` cursor — never re-fetches data from LND
- **Read-only LND access** — the macaroon scope is always read-only

```
cmd/
  satsbook/          # main binary
  lndcheck/          # LND connectivity smoke test
internal/
  lnd/               # LND gRPC client
  db/                # SQLite + queries
  exchange/          # Strike / River / Coinbase CSV parsers
  syncer/            # Background LND polling
  tax/               # Cost basis (Pro)
  web/               # HTTP handlers + HTMX templates
  config/            # Env-var config
  price/             # BTC price fetch + cache
```

## Development

```bash
make build           # build binary
make test            # run all tests
make run             # run with .env file
go vet ./...         # lint
```

Full development guide in [CLAUDE.md](./CLAUDE.md). Branch workflow: never push to main, open a PR, conventional commits, rebase before merge.

## Feedback and roadmap

- **Bugs and feature requests**: [GitHub Issues](https://github.com/satsbook/satsbook/issues)
- **Discussion**: [GitHub Discussions](https://github.com/satsbook/satsbook/discussions)
- **Roadmap / project board**: https://github.com/orgs/satsbook/projects/1

If you run a Lightning node and Satsbook doesn't match your mental model of your stack, tell me — that feedback is the whole point of the free tier.

## License

**Core (this repo)**: [MIT](./LICENSE). Fork it, self-host it, audit it, ship your own build.

**Pro / Power features** (Phase 2+): proprietary. The tax engine and cloud sync are not open-source, but the free tier always will be.

---

**Built by [BrewGator](https://github.com/brewgator) as a side project for sovereign Bitcoiners who want to know what their node is actually earning. Not financial or tax advice.**
