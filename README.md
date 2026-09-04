# 1Sat Stack

A composable BSV indexing server. One Go binary consolidates overlay, indexer, BSV21 token, and ordinal filesystem (ORDFS) services. Each module can run `embedded`, proxy to a `remote` instance, or be `disabled` through config.

## What's inside

- **Overlay engine** — topic managers and lookup services built on [go-overlay-services](https://github.com/bsv-blockchain/go-overlay-services), with GASP sync support
- **Overlay modules** — BSV21 fungible tokens, BAP identity, BSocial, OPNS domain names, OrdLock marketplace
- **Indexer** — output-level script parsing (P2PKH, inscriptions, BSV21, and more) and transaction ingestion
- **ORDFS** — ordinal content serving with optional Redis caching
- **BEEF storage** — tiered transaction storage (LRU cache, Redis, filesystem, JungleBus fallback)
- **Supporting services** — chaintracks block header tracking, Arcade transaction broadcasting, paymail, wallet operations, pubsub (channels, Redis, SSE)
- **Web UIs** — admin panel for runtime configuration, sweep tool, landing page

## Requirements

- Go 1.26+
- [Bun](https://bun.sh) — only if building the admin, sweep, and landing UIs
- Optional backing services: Redis (distributed store and caching), MongoDB (BSocial module), JungleBus (historical chain sync)

The default setup needs none of the optional services — storage uses embedded Badger under `~/.1sat`.

## Quick start

```bash
go build -o server ./cmd/server
./server
```

The server listens on `:8080` with routes under `/1sat`. Open `http://localhost:8080/1sat/admin` to finish setup and manage runtime settings. Health check lives at `/1sat/health`.

`./build.sh` builds the UIs and the server together. A `Dockerfile` covers container builds.

### Flags and environment

| Option | Purpose |
|--------|---------|
| `--config` | Path to a YAML config file (see `config.example.yaml`) |
| `--data-dir` / `ONESAT_DATA_DIR` | Base directory for data files (default `~/.1sat`) |
| `--log-level` | Override log level (`debug`, `info`, `warn`, `error`) |
| `ONESAT_WALLET_SERVER_PRIVATE_KEY` | Server wallet key — keep in the environment, never on disk |

Any config key can be set via environment variables with the `ONESAT_` prefix, e.g. `server.port` becomes `ONESAT_SERVER_PORT`. A `.env` file in the working directory is loaded if present.

## Configuration

Configuration has two layers:

1. **Static** (`config.yaml` or `ONESAT_*` env vars) — infrastructure that must be known at startup: listen port, data directory, storage backends, external service URLs, secrets. Read once at boot.
2. **Config store** (SQLite at `{data_dir}/config.db`) — application settings managed through the admin UI: auth mode, module toggles, sync settings, tuning parameters.

`config.example.yaml` documents the static layer. Most day-to-day settings belong in the admin UI, so a config file is often unnecessary.

## API

Routes mount under the base path (default `/1sat`):

| Prefix | Service |
|--------|---------|
| `/bsv21`, `/bap`, `/bsocial`, `/opns`, `/market` | Overlay module lookups, plus `/{module}/overlay` for engine endpoints |
| `/txo`, `/owner` | Indexed outputs and address history |
| `/beef` | BEEF transaction retrieval |
| `/content` | ORDFS ordinal content |
| `/chaintracks` | Block headers and merkle proofs |
| `/arcade` | Transaction broadcasting |
| `/bsvalias` | Paymail (with `/.well-known/bsvalias` discovery at root) |
| `/admin` | Admin UI and API |
| `/health` | Health check |

Only enabled modules register their routes. The running server serves interactive API docs at `/1sat/docs` and the OpenAPI spec at `/1sat/api-spec/swagger.json`.

Authentication uses BRC-103/104 mutual auth; the admin API additionally requires an admin identity. An API key (`ONESAT_AUTH_API_KEY`) is available for agent and development access.

## Development

```bash
go test ./...                  # run tests
gofmt -s -w . && go vet ./...  # format and vet (required before commit)
./build-docs.sh                # regenerate OpenAPI docs
```

Additional binaries live in `cmd/`: `bsv21-reindex` and `fixproofs` for maintenance tasks.

## Documentation

- `CLAUDE.md` — project conventions and package map
- `docs/architecture/` — overlay engine, BSV21 pipeline, indexing flow, sync routing
- `docs/research/SIGMA_IDENTITY_APPLIANCE_TOPOLOGY.md` — Sigma BAP + BRC-169 appliance boundary and consumer inventory
- `docs/standards/` — config pattern, per-module logging
- `pkg/store/KEYS.md` — storage key reference
