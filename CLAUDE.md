# CLAUDE.md - 1Sat Stack Project Guide

Composable BSV indexing server consolidating overlay, indexer, BSV21, and ORDFS services.
Each package can be `embedded`, `remote`, or `disabled` via config.

## Build, Test & Lint

```bash
go build -o server ./cmd/server          # Build server binary
go test ./...                            # Run all tests
go test ./pkg/ordfs                      # Test one package
go test ./pkg/ordfs -run TestParseContentPath  # Run specific test
gofmt -s -w .                            # Format code (required before commit)
go vet ./...                             # Vet checks
golangci-lint run                        # Lint (if configured)
./build-docs.sh                          # Generate Swagger docs
```

## Entry Points

- `cmd/server/main.go` - Server entry point
- `cmd/server/config.go` - Service initialization and wiring
- `config.yaml` - Static infrastructure config (optional, env vars also work)
- `~/.1sat/config.db` - Runtime config store (managed via admin UI)

## Package Map

| Package | Purpose |
|---------|---------|
| `pkg/store/` | Storage abstraction (Badger, Redis) |
| `pkg/txo/` | Indexed output storage, implements engine.Storage |
| `pkg/beef/` | BEEF transaction storage (filesystem + JungleBus fallback) |
| `pkg/bsv21/` | BSV21 fungible token service + sync pipeline |
| `pkg/bap/` | BAP identity attestation overlay |
| `pkg/bsocial/` | BSocial social data overlay |
| `pkg/opns/` | OPNS domain name overlay |
| `pkg/overlay/` | Overlay engine coordination, topic/lookup management, generic sync worker |
| `pkg/parse/` | Output-level script parsers (P2PKH, inscription, BSV21, etc.) |
| `pkg/topic/` | Topic managers (admission logic for overlay engine) |
| `pkg/lookup/` | Lookup services (indexing/querying for overlay engine) |
| `pkg/indexer/` | Indexer service (parse + ingest transactions) |
| `pkg/ordfs/` | Ordinal filesystem content serving |
| `pkg/jbsync/` | JungleBus subscription pipeline |
| `pkg/pubsub/` | PubSub abstraction (channels, Redis, SSE) |
| `pkg/worker/` | Generic queue worker with configurable concurrency |
| `pkg/owner/` | Owner/address sync from JungleBus |
| `pkg/merkle/` | Merkle proof management and score updates |
| `pkg/types/` | Shared types (HeightScore, etc.) |
| `pkg/dedup/` | Concurrent deduplication (Loader/Saver) |
| `pkg/gasp/` | GASP sync protocol |
| `pkg/wallet/` | Wallet operations |
| `pkg/logging/` | Per-module logging utilities |

## Storage Keys

All keys documented in `pkg/store/KEYS.md`.
Application keys are type-agnostic. Namespace prefixes: `ev:` (event), `tp:` (topic), `q:` (queue), `tm_` (topic manager). Badger handles type discrimination internally.

## Code Style

### Import Ordering

1. Standard library (context, log/slog, os, time, etc.)
2. Local project (github.com/b-open-io/1sat-stack/pkg/...)
3. Third-party (github.com/spf13/viper, github.com/gofiber/fiber/v2, etc.)

```go
import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/gofiber/fiber/v2"
)
```

### Naming

- **Types/Interfaces**: PascalCase (`Config`, `OutputStore`, `Engine`)
- **Exported functions**: PascalCase (`Load`, `Initialize`, `SetDefaults`)
- **Private functions**: camelCase (`parseContentPath`, `loadByTxid`)
- **Constants**: PascalCase or UPPER_SNAKE_CASE (`ModeDisabled`, `lockTTL`)
- **Variables**: camelCase (`logger`, `cfg`, `txid`)

### Error Handling

- Always check errors; wrap with `%w` for error chains
- Provide descriptive messages: `fmt.Errorf("failed to save beef for %s: %w", txid, err)`

### Struct Tags

- `mapstructure` for config structs
- `json` for API responses
- `swagger` for API documentation

### Testing

- Table-driven tests with `t.Run()` subtests
- Test happy paths, error conditions, and edge cases

### Logging

- Use `log/slog` with structured fields
- Pass logger through parameters, not globals
- See `docs/standards/PER_MODULE_LOGGING.md` for per-module log level pattern

### HTTP Handlers

- All handlers must have Swag annotations for OpenAPI docs
- Routes defined without prefixes; consumers decide mount points

### Context

- Always pass `context.Context` to cancellable/long-running operations
- Propagate through method chains

## Configuration Pattern

Two-layer configuration model:

1. **Static layer** (Viper): `config.yaml` or `ONESAT_*` env vars. Read once at startup. Covers infrastructure: listen port, data dir, private key, storage backends.
2. **Config store** (SQLite at `{data_dir}/config.db`): Runtime settings managed via admin UI. Covers application settings: auth mode, overlay toggles, sync settings, tuning parameters.

Only two values live outside both layers:
- `ONESAT_DATA_DIR` / `--data-dir` — needed to locate the config store itself
- `ONESAT_PRIVATE_KEY` — server wallet key (secret, never persisted to disk)

See `docs/standards/CONFIG_GUIDE.md` for the static layer Go pattern:
- Each package has `SetDefaults(v *viper.Viper, prefix string)` and `Initialize(ctx, logger, ...deps) (*Services, error)`
- All defaults in `SetDefaults()`, never in `Initialize()` or business logic
- Config files live with their package, not in a central `config/` directory
- Mode field: `embedded` | `remote` | `disabled`

New config keys that should be editable at runtime need corresponding admin UI exposure.

## Documentation Structure

```
CLAUDE.md                        # This file - project conventions (canonical)
AGENTS.md                       # Points to CLAUDE.md (for OpenCode compatibility)
docs/archive/PLAN.md            # Original consolidation roadmap (archived)
docs/
  architecture/                  # How systems work (current state)
  standards/                     # Patterns to follow (prescriptive)
  audits/                        # Issue tracking, bug reports
  archive/                       # Legacy reference (completed/superseded)
pkg/store/KEYS.md               # Package-level storage key reference
```

### Documentation Rules

- **Naming**: `UPPER_SNAKE_CASE.md` for all docs
- **Root-level**: Only `CLAUDE.md`, `AGENTS.md`, `PLAN.md`
- **Architecture docs**: Describe current system state; update in-place when system changes
- **Standards docs**: Prescriptive patterns; update in-place when pattern evolves
- **Audits**: Move to `archive/` when all issues resolved
- **Plans**: Move to `archive/` when all checklist items complete
- **Package docs**: Keep with their package (e.g., `pkg/store/KEYS.md`)
- **Don't create docs for**: single bug fixes, small refactors, work that fits in a commit message

### Key Reference Docs

| Doc | Purpose |
|-----|---------|
| `docs/standards/CONFIG_GUIDE.md` | Viper config pattern with examples and anti-patterns |
| `docs/standards/PER_MODULE_LOGGING.md` | Per-module log level configuration |
| `docs/architecture/OVERLAY_ARCHITECTURE.md` | Overlay engine, topics, lookups, data flow |
| `docs/architecture/BSV21_PIPELINE.md` | BSV21 token processing pipeline |
| `docs/architecture/INDEXING_ARCHITECTURE.md` | Indexing flow design and parser event plan |
| `docs/architecture/OVERLAY_SYNC_ROUTING.md` | Broadcast vs JungleBus paths, event bridges, per-module strategies |
| `pkg/store/KEYS.md` | All storage keys, prefixes, score format |

## Development Workflow

1. Create feature branch from main
2. Follow code style guidelines above
3. Run tests: `go test ./... -v`
4. Format and vet: `gofmt -s -w . && go vet ./...`
5. Commit with descriptive message
6. Push and create PR

## Common Pitfalls

- **Missing nil checks**: Always check for nil before using objects
- **Forgetting context**: Long-running functions must accept `context.Context`
- **Not wrapping errors**: Use `%w` for proper error chains
- **Untested paths**: Write tests for edge cases and error paths
- **Ignoring formatting**: Run `gofmt` before committing
- **Global state**: Pass dependencies explicitly, not via globals

## Project status

See `docs/plans/STATUS.md` for the current state of all plans and initiatives.

## Documentation layout

- `docs/plans/STATUS.md` — Master index of all plans with current status
- `docs/plans/*.md` — Individual plan documents
- `docs/research/*.md` — Reference research
- Convention rules are in `.claude/rules/docs-convention.md`
