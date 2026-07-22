# Docker Compose Service Split — Plan

Status: **On Hold** — superseded by [microservice-split.md](./microservice-split.md); the compose
generation design (Phases 3–5) remains relevant for the configurator phase.

## Problem

1sat-stack runs as a single Go process — all overlays (BAP, BSocial, OPNS, OrdLock, BSV21), the indexer, ORDFS, wallet, admin UI, and shared infrastructure (Store, BEEF, Chaintracks, TXO, PubSub) are initialized in one binary and wired together via in-process struct references. This works for single-node deployments but creates problems as the system grows:

- A crash or memory spike in one overlay takes down everything
- No independent scaling — can't run two OPNS crawlers without running two of everything
- Resource limits are process-wide, not per-service
- Updates require full restart of all services
- Heavy sync workloads (indexer, BSV21, OPNS crawl) contend with latency-sensitive API serving

## Goal

Split 1sat-stack into independently deployable services managed by Docker Compose. The admin UI configures services in application-domain terms (enabled/disabled, sync settings, resource limits). A compose generation layer renders the runtime configuration into a `docker-compose.yml` and applies it. Users never see raw compose YAML.

## Architecture

### Service Topology

```
┌─────────────────────────────────────────────────────┐
│                   Shared State                       │
│  ┌──────────┐  ┌─────────┐  ┌────────────────────┐  │
│  │  Badger  │  │  Redis  │  │  SQLite/MongoDB    │  │
│  │ (volume) │  │ (svc)   │  │  (volume)          │  │
│  └──────────┘  └─────────┘  └────────────────────┘  │
└─────────────────────────────────────────────────────┘
         ▲           ▲            ▲           ▲
         │           │            │           │
┌────────┴────┐ ┌────┴────┐ ┌─────┴─────┐ ┌────┴──────┐
│   core      │ │ bsv21   │ │  opns     │ │  bap      │
│ (API+admin  │ │ (svc)   │ │  (svc)    │ │  (svc)    │
│  +indexer)  │ │         │ │           │ │           │
└─────────────┘ └─────────┘ └───────────┘ └───────────┘
                  ┌──────────┐ ┌──────────┐
                  │ bsocial  │ │ ordlock  │
                  │ (svc)    │ │ (svc)    │
                  └──────────┘ └──────────┘
```

### Core Service

The **core** process owns the shared infrastructure and the API surface:

| Component | Why it stays in core |
|-----------|---------------------|
| Store (Badger) | Embedded database — can't be a remote process without a network protocol; shared by all services |
| BEEF Storage | Same — filesystem/Badger backed, shared by indexer + overlays |
| Chaintracks | P2P block header tracker — singleton by nature |
| TXO OutputStore | Index of all transaction outputs — shared dependency for indexer + overlays |
| PubSub | Event bus bridging indexer → overlay queues; central coordination point |
| Indexer | Ingest pipeline fed by JungleBus; writes to TXO + BEEF; emits events to PubSub |
| Arcade Client | SSE consumer + broadcast handler; single callback token |
| Admin UI | Config store, settings, log viewer — orchestrates the rest |
| Wallet + Auth | BRC-100 identity for the server; auth middleware for admin |
| Landing/Sweep | Static UIs, minimal footprint |

### Remote Overlay Services

Each overlay module (BAP, BSocial, OPNS, OrdLock, BSV21) becomes a standalone process when `mode: remote` is set. These are the natural split candidates:

- They already share a uniform `ModuleDeps` interface
- Each has its own sync worker (JungleBus subscription + overlay sync)
- Each has its own HTTP routes (lookup queries + overlay submit)
- They communicate with core via gRPC or HTTP API

**Per-service responsibilities:**
- Own overlay engine instance (topic manager + lookup service)
- Run JungleBus subscriber for its subscription ID
- Run overlay sync worker
- Serve lookup query API
- Serve overlay submit endpoint (with body limit)

**Dependencies on core (via network API):**
- TXO output lookups (by outpoint, by txid, by spend)
- BEEF data retrieval
- ChainTracker (current height, merkle paths)
- Store (queue operations, config reads)
- PubSub (subscribe to event patterns for overlay-specific queues)
- Broadcaster (submit transactions to arcade via core)

### Infrastructure Services (Docker-managed)

| Service | Image | Purpose |
|---------|-------|---------|
| `redis` | `redis:7-alpine` | Shared store backend (when `store.provider: redis`), PubSub backend, BEEF cache tier |
| `mongodb` | `mongo:7` | BSocial data store (only when BSocial enabled) |

Badger and SQLite stay as volume mounts — they're embedded in the processes that use them, not separate services.

### Network API Between Core and Remote Services

The core process exposes internal APIs that remote services call:

| API | Method | Called by |
|-----|--------|----------|
| `/internal/txo/outpoint/:outpoint` | GET | All overlay services |
| `/internal/txo/txid/:txid` | GET | All overlay services |
| `/internal/beef/:txid` | GET | All overlay services |
| `/internal/chaintracks/height` | GET | All overlay services |
| `/internal/chaintracks/merkle/:txid/:height` | GET | All overlay services |
| `/internal/store/:key` | GET/PUT | All overlay services (queue ops, config) |
| `/internal/pubsub/:topic` | GET (SSE) | All overlay services (event subscription) |
| `/internal/broadcast` | POST | All overlay services (tx submission) |

These are internal-only — not exposed to the public API. Authenticated via a shared internal token (compose secret).

## Implementation Plan

### Phase 1: Remote Mode for Overlay Modules

The overlay packages already have `mode` constants (`disabled`/`embedded`). `remote` is the third mode. Today only BSV21 has a `remote` branch (partially implemented). The rest panic with "TODO."

**Tasks:**

- [ ] Define `pkg/remoting/` package with a gRPC or HTTP client interface that implements the same `ModuleDeps` contract via network calls to core
  - `RemoteOutputStore` wrapping core's TXO API
  - `RemoteBeefStorage` wrapping core's BEEF API
  - `RemoteChainTracker` wrapping core's Chaintracks API
  - `RemoteStore` wrapping core's Store API
  - `RemotePubSub` wrapping core's PubSub SSE endpoint
  - `RemoteBroadcaster` wrapping core's broadcast API
- [ ] Implement `remote` mode in each overlay package's `Initialize()`:
  - BAP (`pkg/bap/config.go`)
  - BSocial (`pkg/bsocial/config.go`)
  - OPNS (`pkg/opns/config.go`)
  - OrdLock (`pkg/ordlock/config.go`)
  - BSV21 (`pkg/bsv21/config.go` — already has the branch, complete it)
- [ ] Add core-side `/internal/*` API routes in `cmd/server/config.go` `RegisterRoutes`
- [ ] Add internal auth token to Config struct (generated on first run, stored in config.db)
- [ ] Tests: each overlay module can run as remote against a running core

### Phase 2: Per-Service Binaries

Each overlay gets its own `cmd/` entry point that:
1. Loads config (static layer from env vars or config file)
2. Connects to core via internal API
3. Initializes its overlay engine in `remote` mode
4. Starts JungleBus subscriber + sync worker
5. Serves its HTTP routes on its own port

**Tasks:**

- [ ] `cmd/bap/main.go`
- [ ] `cmd/bsocial/main.go`
- [ ] `cmd/opns/main.go`
- [ ] `cmd/ordlock/main.go`
- [ ] `cmd/bsv21/main.go`
- [ ] Shared `cmd/common/` for config loading, core connection, logging setup
- [ ] Each binary has a `--core-url` flag pointing to the core internal API
- [ ] Each binary has a `--port` flag for its own HTTP listener

### Phase 3: Compose Generation

A Go package (`pkg/compose/` or a standalone `cmd/1sat-deploy/`) that:
1. Reads the SQLite config store (same `configpkg.Store` interface)
2. Reads the static config (Viper)
3. Renders a `docker-compose.yml` from service templates
4. Runs `docker compose up -d` (or writes the file for manual use)

The generation logic maps `RuntimeConfig` fields to compose services:

| Config Key | Compose Effect |
|------------|---------------|
| `bap.enabled = true`, `bap.mode = remote` | Add `bap` service, depends_on core |
| `bsv21.enabled = true`, `bsv21.mode = remote` | Add `bsv21` service |
| `store.provider = redis` | Add `redis` service, core depends_on it |
| `overlay.bsocial.mongo_url` set | Add `mongodb` service |
| `bsv21.sync.concurrency = 16` | Set `ONESAT_BSV21_SYNC_CONCURRENCY=16` env on bsv21 service |
| `ordlock.sync.subscription_id` set | Set `ONESAT_ORDLOCK_SYNC_SUBSCRIPTION_ID=...` env |

**Tasks:**

- [ ] Design `pkg/compose/` types: `Service`, `Network`, `Volume`, `Secret`
- [ ] Service registry: map of service name → template (image, ports, env, depends_on, healthcheck)
- [ ] Config-to-compose mapper: reads `RuntimeConfig`, enables/disables services, injects env vars
- [ ] YAML renderer: writes valid `docker-compose.yml`
- [ ] CLI tool `cmd/1sat-deploy/main.go`:
  - `1sat-deploy render` — write compose file to stdout or path
  - `1sat-deploy up` — render + `docker compose up -d`
  - `1sat-deploy down` — `docker compose down`
  - `1sat-deploy status` — `docker compose ps` with service health
- [ ] Docker images: multi-stage Dockerfile per service (or single Dockerfile with build args for the entry point)

### Phase 4: Admin UI Integration

The admin UI gets a "Deployment" section that:
- Shows each service as a card with status (running/stopped/unknown)
- Toggles between `embedded` / `remote` / `disabled` for each overlay
- Sets per-service resource limits (memory, CPU) via compose
- "Apply Changes" button calls `1sat-deploy up` (or an internal API endpoint that triggers it)
- Shows logs per service (streaming from docker logs or the existing log store)

**Tasks:**

- [ ] New `/admin/deployment` API endpoints:
  - `GET /admin/deployment/services` — list services with compose status
  - `POST /admin/deployment/apply` — render + restart compose
  - `GET /admin/deployment/logs/:service` — stream logs
- [ ] Admin UI "Deployment" page
- [ ] Wire "Apply Changes" to `1sat-deploy up`
- [ ] Service health checks in compose (HTTP health endpoint per service)

### Phase 5: Single Dockerfile Multi-Target

Rather than maintaining per-service Dockerfiles, use a single Dockerfile with build args:

```dockerfile
 ARG SERVICE=server
 RUN go build -o /app/service ./cmd/${SERVICE}
 CMD ["/app/service"]
```

Compose references the same image with different `command` or `environment.SERVICE` values. This keeps the build simple and all services share the same binary codebase (just different entry points).

**Tasks:**

- [ ] Update `Dockerfile` to accept `SERVICE` build arg
- [ ] Add `cmd/bap/`, `cmd/bsocial/`, etc. as valid build targets
- [ ] `.dockerignore` for clean build context
- [ ] Healthcheck endpoints per service binary

## Service Descriptor Format

Each service registers itself with a descriptor that the compose generator consumes:

```go
type ServiceDescriptor struct {
    Name        string            // "bap", "bsv21", etc.
    Binary      string            // "cmd/bap"
    Port        int               // 8081
    DependsOn   []string          // ["core"]
    Envs        map[string]string // config key → env var name mapping
    HealthCheck string            // "/health"
    Image       string            // defaults to "1sat-stack:${SERVICE}"
}
```

This registry lives in `pkg/compose/registry.go` and is the single source of truth for what services exist and how they're configured. The admin UI reads this registry to render the deployment page.

## Config Flow

```
Admin UI
    │
    ▼
config.db (SQLite RuntimeConfig)
    │
    ▼
1sat-deploy render
    │
    ├── Static config (config.yaml / ONESAT_ env)
    │
    ▼
docker-compose.yml
    │
    ▼
docker compose up -d
    │
    ├── core service (env: ONESAT_BAP_MODE=remote)
    ├── bap service (env: ONESAT_BAP_MODE=remote, CORE_URL=http://core:8080)
    ├── bsv21 service (env: ONESAT_BSV21_MODE=remote, ...)
    └── redis/mongodb (if needed)
```

Key principle: the admin UI writes application config (enable BAP, set sync concurrency). The deploy tool translates that into infrastructure config (compose service, env vars, resource limits). The user never sees the translation layer.

## Current Mode Pattern Summary

| Package | Modes | Default | Remote Implemented |
|---------|-------|---------|-------------------|
| Store | embedded, disabled | embedded | No (uses shared volume) |
| BEEF | embedded, remote, disabled | embedded | No ("TODO") |
| TXO | embedded, remote, disabled | embedded | No ("TODO") |
| PubSub | embedded, remote, disabled | embedded | No ("TODO") |
| BSV21 | embedded, remote, disabled | disabled | Partial |
| BAP | embedded, disabled | disabled | No |
| BSocial | embedded, disabled | disabled | No |
| OPNS | embedded, disabled | disabled | No |
| OrdLock | embedded, disabled | disabled | No |
| Indexer | embedded, disabled | embedded | N/A (stays in core) |
| Owner | embedded, disabled | disabled | N/A (stays in core) |
| Chaintracks | embedded, remote, disabled | embedded | N/A (stays in core) |

Packages staying in core (Store, BEEF, TXO, PubSub, Indexer, Owner, Chaintracks) don't need `remote` mode. Overlay modules (BAP, BSocial, OPNS, OrdLock, BSV21) are the split targets.

## Open Questions

1. **gRPC vs HTTP for internal API?** HTTP is simpler to start with and the overlay engine already speaks HTTP. gRPC would be better for streaming (PubSub events) but adds a dependency. Recommendation: start with HTTP + SSE, add gRPC later only if latency demands it.

2. **Shared Badger volume vs Redis?** If overlay services are remote, they can't share the embedded Badger volume — they'd need Redis or an HTTP store API. Two paths:
   - **Path A:** Core exposes Store + BEEF + TXO via HTTP API. Remote services call these. Badger stays in core only.
   - **Path B:** Switch to Redis as the shared store backend (already supported: `store.provider: redis`). Each service connects to Redis directly. Simpler, but Redis becomes required for multi-service mode.
   - Recommendation: Path B for simplicity. Redis is already optional; it becomes required when any overlay is `remote`. Embedded mode continues using Badger.

3. **Config store access for remote services?** Remote services need to read config (sync settings, subscription IDs). Options:
   - Each service reads its config from env vars (compose sets them from config.db)
   - Each service connects to the same SQLite config.db via shared volume
   - Each service fetches config from core's admin API
   - Recommendation: env vars set by compose generation. The config.db is the source of truth; the deploy tool reads it and sets env vars per service. No shared volume for config.

4. **Log aggregation?** Each service could write to its own SQLite log DB or to stdout (docker logs). Recommendation: stdout + `docker logs` / Docker's logging drivers. The admin log viewer can use `docker compose logs` or a log aggregation sidecar if needed later.

## Risks

- **Network latency:** In-process calls become HTTP round-trips. TXO lookups are the hottest path — overlay sync workers call them per-transaction. Mitigation: batch APIs + connection pooling.
- **State consistency:** Remote services reading from Redis (Path B) see eventual consistency. Store operations that were atomic in Badger become separate Redis operations. Mitigation: use Redis transactions/pipelines where needed.
- **Complexity:** Docker Compose adds a deployment layer. `1sat-deploy` is new code to maintain. Single-binary mode must remain fully functional.
- **Dual code paths:** `embedded` and `remote` modes for each overlay means two initialization paths. Testing matrix grows. Mitigation: the `ModuleDeps` interface already abstracts this; `remote` mode just swaps the implementation.

## Non-Goals

- Kubernetes support (can be added later via the same config → manifest generation pattern)
- Service mesh / sidecar injection
- Multiple core instances (horizontal scaling of core)
- Dynamic service discovery (compose DNS is sufficient)