# 1sat-stack Consolidation Plan

Create a composable server by copying and consolidating from sibling directories:
- `../overlay`
- `../bsv21-overlay`
- `../1sat-indexer`
- `../go-ordfs-server`

## Goals

- Single composable server where packages can be toggled on/off via config
- Shared infrastructure (storage, pubsub, chaintracks, merkle) with explicit dependency injection
- Embedded config pattern (all packages compiled in, `mode` field controls: `embedded`/`remote`/`disabled`)
- Follow established CONFIG_GUIDE.md pattern throughout
- Copy code into 1sat-stack (originals will be archived later by user)
- **All HTTP handlers must have Swag annotations** for automatic OpenAPI documentation

---

## Implementation Progress

### Status: Phase 1 - Foundation (COMPLETE)

| Task | Status | Notes |
|------|--------|-------|
| Project scaffolding (go.mod, directories) | DONE | go.mod with dependencies, pkg/ structure |
| pkg/store/ (from overlay/queue/) | DONE | Store interface, BadgerStore, Search, Config |
| pkg/dedup/ | DONE | Loader/Saver for concurrent deduplication |
| pkg/beef/ (from overlay/beef/) | DONE | Storage, LRU, Filesystem, JungleBus, Routes, Config |
| pkg/pubsub/ (from overlay/pubsub/) | DONE | PubSub interface, ChannelPubSub, SSEManager, Routes, Config |
| pkg/txo/ (from overlay/storage/) | DONE | OutputStore, EventDataStorage, Owner, Search, Config |
| pkg/sync/ (from overlay/sync/) | DEFERRED | Will implement when needed for Phase 2+ |
| pkg/topics/ (from overlay/topics/) | DEFERRED | Will implement when needed for Phase 2+ |
| cmd/server/config.go | DONE | Full config with all embedded packages |
| cmd/server/main.go | DONE | Fiber server with graceful shutdown |
| Compile test | DONE | `go build ./...` passes |

### Status: Phase 1b - New Services (COMPLETE)

| Task | Status | Notes |
|------|--------|-------|
| pkg/worker/ | DONE | Generic queue worker with configurable concurrency |
| pkg/merkle/ | DONE | MerkleService, Arc callback handling, Routes, Config |
| Compile test | DONE | `go build ./...` passes |

### Clarifications (from user discussion)

1. **Upgrade as we copy** - Don't copy old patterns, apply CONFIG_GUIDE.md pattern as we go
2. **Mode field only** - Use `mode: embedded|remote|disabled` (no separate `enabled` flag)
3. **Naming mapping**:
   - overlay's `queue/` → `pkg/store/` (low-level KV storage)
   - overlay's `storage/` → `pkg/txo/` (indexed outputs)
4. **Tests last** - Skip tests until everything is complete, keep them focused
5. **Simple code** - Avoid unnecessary abstractions, public struct properties are fine, getters/setters only when needed

### Issues & Decisions Log

| Issue | Decision | Status |
|-------|----------|--------|
| (none yet) | | |

---

## Directory Structure

```
1sat-stack/
├── cmd/server/
│   ├── main.go              # Entry point, initialization
│   └── config.go            # Load() for server config
│
├── pkg/
│   ├── store/               # Storage engine (Badger, Redis, MongoDB) - internal only
│   │   ├── interface.go
│   │   ├── badger/
│   │   ├── redis/
│   │   └── config/
│   │
│   ├── beef/                # BEEF storage (raw tx, proof, beef format)
│   │   ├── interface.go     # BeefService interface
│   │   ├── storage.go       # Local implementation
│   │   ├── client/          # HTTP client (remote)
│   │   ├── routes/          # HTTP handlers
│   │   └── config/
│   │
│   ├── pubsub/              # PubSub abstraction (local channels, Redis, SSE client)
│   │   ├── interface.go     # PubSub interface (Subscribe/Publish)
│   │   ├── channels/        # Local in-memory implementation
│   │   ├── redis/           # Redis pub/sub implementation
│   │   ├── client/          # SSE-backed remote client
│   │   ├── routes/          # SSE endpoint (for remote clients)
│   │   └── config/
│   │
│   ├── txo/                 # TXO storage + queries
│   │   ├── interface.go     # TxoService interface
│   │   ├── store.go         # Local implementation
│   │   ├── client/          # HTTP client (remote)
│   │   ├── routes/          # HTTP handlers
│   │   └── config/
│   │
│   ├── merkle/              # MerkleService (proof management)
│   │   ├── interface.go     # Optional query interface
│   │   ├── service.go       # Local implementation
│   │   ├── routes/          # ARC callback route
│   │   └── config/
│   │
│   ├── worker/              # Generic worker pattern - internal only
│   │   ├── worker.go
│   │   └── config/
│   │
│   ├── sync/                # Peer sync managers - internal only
│   │   ├── libp2p/
│   │   └── gasp/
│   │
│   ├── topics/              # ActiveTopics cache - internal only
│   │   └── active.go
│   │
│   ├── overlay/             # Overlay engine coordination
│   │   ├── registry.go      # EngineRegistry - coordinates all registries
│   │   ├── topic_registry.go    # TopicManagerRegistry
│   │   ├── lookup_registry.go   # LookupServiceRegistry
│   │   └── config/
│   │
│   ├── indexer/             # Indexer service
│   │   ├── interface.go     # IndexerService interface
│   │   ├── indexer.go       # Local implementation
│   │   ├── client/          # HTTP client (remote)
│   │   ├── routes/          # HTTP handlers
│   │   ├── idx/             # Indexer internals, scoring, IngestTx
│   │   ├── mod/             # Protocol parsers (p2pkh, lock, inscription, etc.)
│   │   └── config/
│   │
│   ├── bsv21/               # BSV21 token service
│   │   ├── interface.go     # BSV21Service interface
│   │   ├── service.go       # Local implementation
│   │   ├── client/          # HTTP + SSE client (remote)
│   │   ├── routes/          # HTTP handlers
│   │   ├── lookups/         # BSV21 LookupService
│   │   ├── topics/          # BSV21 TopicManager
│   │   ├── peer/            # Per-token peer config
│   │   └── config/
│   │
│   └── ordfs/               # ORDFS content service
│       ├── interface.go     # Optional OrdfsService interface
│       ├── service.go       # Local implementation
│       ├── client/          # HTTP client (remote) - optional
│       ├── routes/          # HTTP handlers (content delivery)
│       ├── handlers/
│       ├── loader/
│       ├── cache/
│       └── config/
│
├── config.yaml              # Default configuration
└── go.mod
```

---

## Package Services

### MerkleService (`pkg/merkle/`)

Unified service for transaction proof management. Handles all merkle-related state changes.

**Inputs (event sources):**
1. **ARC callback route** (`POST /callback`) - receives tx status changes, new merkle proofs
2. **Arcade subscription** - direct subscription to Arcade's tx status events
3. **Block notifications** (chaintracks) - trigger state transitions (Validated → Immutable)

**Actions:**
1. **Update merkle paths** - store new proofs when they arrive from ARC
2. **Update scores** - recalculate `blockHeight * 1e9 + blockIndex` on outputs AND events
3. **Update merkle state** - Unmined → Validated → Immutable
4. **Rollback invalidated** - remove outputs with MerkleStateInvalidated (reorg victims)

**Note:** No re-indexing/re-parsing needed. Protocol data doesn't change - only proof metadata and scores.

```go
type MerkleService struct {
    storage     *storage.EventDataStorage
    chaintracks chaintracks.Chaintracks
    pubsub      pubsub.PubSub
    logger      *slog.Logger
}

func (ms *MerkleService) Start(ctx context.Context) error
func (ms *MerkleService) Stop()
```

### Worker (`pkg/worker/`)

Generic worker pattern. Consumes from scored set, calls handler with configurable concurrency.

```go
type WorkerConfig struct {
    Store       store.Store
    Key         string                    // Sorted set key to consume from
    Concurrency int
    Handler     func(ctx, txid string) error
    OnError     func(ctx, txid string, err error)
}

type Worker struct { ... }

func NewWorker(cfg *WorkerConfig) *Worker
func (w *Worker) Start(ctx context.Context) error  // With catchup
func (w *Worker) Stop()
```

### TxoStore (`pkg/txo/`)

Indexed output storage and querying. Renamed from `events/` - this is really about TXOs, not pub/sub events.

**Storage:**
- Output metadata (height, idx, satoshis, owners)
- Spend tracking
- Tag-specific data (protocol-parsed data)
- Indexed keys for searching (owners, txids, custom events)

**Routes:**
- Direct lookups by outpoint
- Batch lookups
- By-txid queries
- Search by indexed key (with unspent filtering)

**Key insight:** "Events" in the overlay context are really just indexed keys on outputs. The term "events" was confusing because it conflicted with pub/sub events. Users search by providing the full key (including any topic prefix if needed).

```go
type OutputStore struct {
    Queue     queue.QueueStorage
    PubSub    pubsub.PubSub
    BeefStore *beef.Storage
    Prefix    string  // Optional topic prefix for key scoping
}
```

---

## Service Interfaces (Local vs Remote)

Following the patterns established by `go-chaintracks` and `arcade`, packages that expose functionality consumable by other services should provide:

1. **Interface** - Defines the contract (same API for local or remote)
2. **Local implementation** - Direct in-process calls (embedded mode)
3. **Remote client** - HTTP/SSE client that implements the same interface

This allows the same code to work whether services are co-located in one binary or distributed across systems.

### Package Structure Pattern

```
pkg/beef/
├── interface.go      # BeefService interface
├── storage.go        # Local implementation (embedded)
├── client/
│   └── client.go     # HTTP client implementation
├── routes/
│   └── routes.go     # HTTP handlers (for remote clients)
└── config/
    └── config.go     # Config + Initialize
```

### Service Interface Requirements

| Package | Needs Interface? | Subscription? | Notes |
|---------|-----------------|---------------|-------|
| `pkg/store/` | No | - | Internal infrastructure only |
| `pkg/beef/` | **Yes** | No | Raw tx/proof retrieval |
| `pkg/txo/` | **Yes** | Maybe | Output queries, key subscriptions |
| `pkg/pubsub/` | **Yes** | **Yes** | Core subscription abstraction |
| `pkg/merkle/` | Maybe | No | State queries if deployed separately |
| `pkg/indexer/` | **Yes** | No | Parse/ingest operations |
| `pkg/bsv21/` | **Yes** | Yes | Token queries + events |
| `pkg/ordfs/` | Maybe | No | HTTP GET is natural interface |

### PubSub Interface (Core Abstraction)

The `PubSub` interface abstracts subscription mechanics. Consumers subscribe to topics and receive channels - they don't know or care if it's backed by local channels, Redis, or SSE.

```go
// pkg/pubsub/interface.go
type Event struct {
    Topic string
    Data  []byte
}

type PubSub interface {
    Publish(ctx context.Context, topic string, data []byte) error
    Subscribe(ctx context.Context, topics []string) (<-chan *Event, error)
    Unsubscribe(ch <-chan *Event) error
}
```

**Implementations:**

```go
// Local - in-memory channels with fan-out
ps := channels.NewPubSub()

// Redis - for multi-instance deployments
ps := redispubsub.New(redisClient)

// Remote - SSE transport hidden inside
ps := pubsubclient.New("http://server:8080/sse")
```

**Usage is identical regardless of implementation:**

```go
ch, _ := ps.Subscribe(ctx, []string{"txo:abc123", "bsv21:token1"})
for event := range ch {
    // process event
}
```

**TypeScript equivalent:**

```typescript
interface PubSub {
    publish(topic: string, data: Uint8Array): Promise<void>
    subscribe(topics: string[]): AsyncIterable<Event>
}

// SSE details hidden - consumer just iterates
for await (const event of pubsub.subscribe(["txo:abc123"])) {
    // process event
}
```

### BeefService Interface

```go
// pkg/beef/interface.go
type BeefService interface {
    GetRawTx(ctx context.Context, txid string) ([]byte, error)
    GetProof(ctx context.Context, txid string) ([]byte, error)
    GetBeef(ctx context.Context, txid string) ([]byte, error)
    GetOutput(ctx context.Context, txid string, vout uint32) ([]byte, error)
}
```

### TxoService Interface

```go
// pkg/txo/interface.go
type TxoService interface {
    // Single lookups
    GetTxo(ctx context.Context, outpoint string, tags []string) (*Txo, error)
    GetSpend(ctx context.Context, outpoint string) (*Spend, error)

    // By transaction
    GetTxosByTxid(ctx context.Context, txid string, tags []string) ([]*Txo, error)

    // Search by indexed key
    Search(ctx context.Context, key string, unspent bool, limit int) ([]*Txo, error)

    // Batch operations
    GetTxos(ctx context.Context, outpoints []string, tags []string) ([]*Txo, error)
    GetSpends(ctx context.Context, outpoints []string) ([]*Spend, error)
}
```

### IndexerService Interface

```go
// pkg/indexer/interface.go
type IndexerService interface {
    ParseTx(ctx context.Context, txBytes []byte) (*ParseResult, error)
    ParseTxid(ctx context.Context, txid string) (*ParseResult, error)
    IngestTx(ctx context.Context, txid string) error
}
```

### BSV21Service Interface

```go
// pkg/bsv21/interface.go
type BSV21Service interface {
    GetToken(ctx context.Context, id string) (*Token, error)
    GetBalance(ctx context.Context, tokenId, address string) (*Balance, error)
    ListTokens(ctx context.Context, limit, offset int) ([]*Token, error)

    // Real-time token events (uses PubSub internally)
    Subscribe(ctx context.Context, tokenId string) (<-chan *TokenEvent, error)
    Unsubscribe(ch <-chan *TokenEvent) error
}
```

### Implementation Verification

All implementations should include compile-time interface verification:

```go
var _ BeefService = (*Storage)(nil)       // Local
var _ BeefService = (*Client)(nil)        // Remote
```

---

## Config Pattern (follows CONFIG_GUIDE.md)

Each package is an embedded config struct with SetDefaults and Initialize. Packages are enabled/disabled via their own config (e.g., `enabled: true` or presence of required fields).

### cmd/server/config.go

```go
type Config struct {
    Server      ServerConfig                `mapstructure:"server"`

    // Shared external services
    P2P         p2pconfig.Config            `mapstructure:"p2p"`          // go-teranode-p2p-client
    Arcade      arcadeconfig.Config         `mapstructure:"arcade"`       // arcade
    Chaintracks chaintracksconfig.Config    `mapstructure:"chaintracks"`  // go-chaintracks

    // Internal packages
    Store       storeconfig.Config          `mapstructure:"store"`
    PubSub      pubsubconfig.Config         `mapstructure:"pubsub"`
    Beef        beefconfig.Config           `mapstructure:"beef"`
    Txo         txoconfig.Config            `mapstructure:"txo"`
    Merkle      merkleconfig.Config         `mapstructure:"merkle"`
    Indexer     indexerconfig.Config        `mapstructure:"indexer"`
    BSV21       bsv21config.Config          `mapstructure:"bsv21"`
    ORDFS       ordfsconfig.Config          `mapstructure:"ordfs"`
}

type ServerConfig struct {
    Port     int    `mapstructure:"port"`
    LogLevel string `mapstructure:"log_level"`
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault("server.port", 8080)
    v.SetDefault("server.log_level", "info")

    // Shared external services
    c.P2P.SetDefaults(v, "p2p")
    c.Arcade.SetDefaults(v, "arcade")
    c.Chaintracks.SetDefaults(v, "chaintracks")

    // Internal packages
    c.Store.SetDefaults(v, "store")
    c.PubSub.SetDefaults(v, "pubsub")
    c.Beef.SetDefaults(v, "beef")
    c.Txo.SetDefaults(v, "txo")
    c.Merkle.SetDefaults(v, "merkle")
    c.Indexer.SetDefaults(v, "indexer")
    c.BSV21.SetDefaults(v, "bsv21")
    c.ORDFS.SetDefaults(v, "ordfs")
}
```

### Package config pattern

Each package with a service interface follows this pattern:

```go
// pkg/beef/config/config.go
const (
    ModeDisabled = "disabled"
    ModeEmbedded = "embedded"
    ModeRemote   = "remote"
)

type Config struct {
    Mode string `mapstructure:"mode"` // disabled, embedded, remote
    URL  string `mapstructure:"url"`  // Required for remote mode

    // Routes config (only relevant for embedded mode)
    Routes RoutesConfig `mapstructure:"routes"`

    // Package-specific settings
    Chain []string `mapstructure:"chain"`  // e.g., for beef storage chain
}

type RoutesConfig struct {
    Enabled bool   `mapstructure:"enabled"` // Whether to serve HTTP routes
    Prefix  string `mapstructure:"prefix"`  // Route prefix override (optional)
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    if prefix != "" { prefix = prefix + "." }
    v.SetDefault(prefix+"mode", ModeDisabled)
    v.SetDefault(prefix+"routes.enabled", true)
    v.SetDefault(prefix+"routes.prefix", "")
}

// pkg/beef/config/services.go
type Services struct {
    Service beef.BeefService  // Interface - could be local or remote
    Routes  *routes.Routes    // nil if routes not enabled or remote mode
}

func (c *Config) Initialize(
    ctx context.Context,
    logger *slog.Logger,
    store store.Store,          // Required for embedded mode, ignored for remote
) (*Services, error) {
    if c.Mode == ModeDisabled {
        return nil, nil
    }

    var svc beef.BeefService

    switch c.Mode {
    case ModeRemote:
        svc = beefclient.New(c.URL)
    case ModeEmbedded:
        svc = beef.NewStorage(store, c.Chain)
    default:
        return nil, fmt.Errorf("unknown mode: %s", c.Mode)
    }

    var rts *routes.Routes
    if c.Mode == ModeEmbedded && c.Routes.Enabled {
        rts = routes.NewRoutes(svc)
    }

    return &Services{Service: svc, Routes: rts}, nil
}

func (s *Services) Close() error

// pkg/beef/routes/routes.go
type Routes struct { service beef.BeefService }
func NewRoutes(s beef.BeefService) *Routes
func (r *Routes) Register(router fiber.Router)
```

### Mode Summary

| Mode | Service | Routes |
|------|---------|--------|
| `disabled` | nil | nil |
| `embedded` | Local implementation | Configurable via `routes.enabled` |
| `remote` | HTTP client | N/A (consuming remote) |

### main.go initialization

```go
func main() {
    cfg, _ := Load()
    ctx := context.Background()
    logger := slog.New(...)

    var closers []io.Closer

    // All packages are optional - mode drives what gets initialized
    // Initialize returns nil for disabled mode
    // Dependencies are passed explicitly; nil = not available

    // Shared external services (initialized first, shared by multiple packages)
    p2pSvc, _ := cfg.P2P.Initialize(ctx, logger)
    if p2pSvc != nil {
        closers = append(closers, p2pSvc)
    }

    var p2pClient *p2p.Client
    if p2pSvc != nil {
        p2pClient = p2pSvc.Client
    }

    arcadeSvc, _ := cfg.Arcade.Initialize(ctx, logger, nil, p2pClient)
    if arcadeSvc != nil {
        closers = append(closers, arcadeSvc)
    }

    chaintracksSvc, _ := cfg.Chaintracks.Initialize(ctx, logger, p2pClient)
    if chaintracksSvc != nil {
        closers = append(closers, chaintracksSvc)
    }

    // Internal packages - each returns nil for disabled mode
    storeSvc, _ := cfg.Store.Initialize(ctx, logger)
    if storeSvc != nil {
        closers = append(closers, storeSvc)
    }

    pubsubSvc, _ := cfg.PubSub.Initialize(ctx, logger)
    if pubsubSvc != nil {
        closers = append(closers, pubsubSvc)
    }

    // Helper to safely get nested services
    var store store.Store
    if storeSvc != nil { store = storeSvc.Store }
    var pubsub pubsub.PubSub
    if pubsubSvc != nil { pubsub = pubsubSvc.Service }

    beefSvc, _ := cfg.Beef.Initialize(ctx, logger, store)
    if beefSvc != nil {
        closers = append(closers, beefSvc)
    }

    var beefService beef.BeefService
    if beefSvc != nil { beefService = beefSvc.Service }

    txoSvc, _ := cfg.Txo.Initialize(ctx, logger, store, beefService, pubsub)
    if txoSvc != nil {
        closers = append(closers, txoSvc)
    }

    var chaintracks chaintracks.Chaintracks
    if chaintracksSvc != nil { chaintracks = chaintracksSvc.Chaintracks }

    merkleSvc, _ := cfg.Merkle.Initialize(ctx, logger, store, pubsub, chaintracks)
    if merkleSvc != nil {
        closers = append(closers, merkleSvc)
    }

    indexerSvc, _ := cfg.Indexer.Initialize(ctx, logger, store, beefService, pubsub)
    if indexerSvc != nil {
        closers = append(closers, indexerSvc)
    }

    bsv21Svc, _ := cfg.BSV21.Initialize(ctx, logger, store, pubsub)
    if bsv21Svc != nil {
        closers = append(closers, bsv21Svc)
    }

    ordfsSvc, _ := cfg.ORDFS.Initialize(ctx, logger, beefService)
    if ordfsSvc != nil {
        closers = append(closers, ordfsSvc)
    }

    defer func() { for _, c := range closers { c.Close() } }()

    // Register routes only if Routes is non-nil (embedded mode + routes.enabled: true)
    app := fiber.New()

    if beefSvc != nil && beefSvc.Routes != nil {
        prefix := firstNonEmpty(cfg.Beef.Routes.Prefix, "/tx")
        beefSvc.Routes.Register(app.Group(prefix))
    }
    if txoSvc != nil && txoSvc.Routes != nil {
        prefix := firstNonEmpty(cfg.Txo.Routes.Prefix, "/txo")
        txoSvc.Routes.Register(app.Group(prefix))
    }
    if pubsubSvc != nil && pubsubSvc.Routes != nil {
        prefix := firstNonEmpty(cfg.PubSub.Routes.Prefix, "/sse")
        pubsubSvc.Routes.Register(app.Group(prefix))
    }
    if merkleSvc != nil && merkleSvc.Routes != nil {
        prefix := firstNonEmpty(cfg.Merkle.Routes.Prefix, "/arc")
        merkleSvc.Routes.Register(app.Group(prefix))
    }
    if indexerSvc != nil && indexerSvc.Routes != nil {
        prefix := firstNonEmpty(cfg.Indexer.Routes.Prefix, "/idx")
        indexerSvc.Routes.Register(app.Group(prefix))
    }
    if bsv21Svc != nil && bsv21Svc.Routes != nil {
        prefix := firstNonEmpty(cfg.BSV21.Routes.Prefix, "/bsv21")
        bsv21Svc.Routes.Register(app.Group(prefix))
    }
    if ordfsSvc != nil && ordfsSvc.Routes != nil {
        prefix := firstNonEmpty(cfg.ORDFS.Routes.Prefix, "/ordfs")
        ordfsSvc.Routes.Register(app.Group(prefix))
    }

    app.Listen(fmt.Sprintf(":%d", cfg.Server.Port))
}

func firstNonEmpty(values ...string) string {
    for _, v := range values {
        if v != "" { return v }
    }
    return ""
}
```

---

## Configuration Examples

### Example 1: Full Monolith (All Local, All Routes Served)

Everything runs in one process, all APIs exposed.

```yaml
server:
  port: 8080
  log_level: info

# Shared external services
p2p:
  mode: embedded
  network: main

arcade:
  mode: embedded

chaintracks:
  mode: embedded

# Internal packages - all embedded, all routes enabled (default)
store:
  mode: embedded
  url: "badger://~/.1sat-stack/store"

pubsub:
  mode: embedded
  url: "channels://"
  routes:
    enabled: true  # Serve SSE endpoint

beef:
  mode: embedded
  chain: ["lru://?size=100mb", "~/.1sat-stack/beef", "junglebus://"]
  routes:
    enabled: true

txo:
  mode: embedded
  routes:
    enabled: true

merkle:
  mode: embedded
  routes:
    enabled: true

indexer:
  mode: embedded
  indexers: [p2pkh, lock, inscription, ordlock, bsv20, bsv21, origin]
  routes:
    enabled: true

bsv21:
  mode: embedded
  routes:
    enabled: true

ordfs:
  mode: embedded
  routes:
    enabled: true
```

### Example 2: Embedded Library Mode (No Routes)

Use as a library - no HTTP server, just in-process access.

```yaml
server:
  port: 0  # No HTTP server

store:
  mode: embedded
  url: "badger://~/.1sat-stack/store"

pubsub:
  mode: embedded
  url: "channels://"
  routes:
    enabled: false  # No SSE endpoint

beef:
  mode: embedded
  chain: ["~/.1sat-stack/beef"]
  routes:
    enabled: false  # No HTTP routes

txo:
  mode: embedded
  routes:
    enabled: false

indexer:
  mode: embedded
  indexers: [p2pkh, inscription]
  routes:
    enabled: false

# Omitted packages default to mode: disabled
```

### Example 3: Microservices (Remote Clients)

Connect to remote services instead of running locally.

```yaml
server:
  port: 8080
  log_level: info

# store: disabled (not needed when using remote services)

pubsub:
  mode: remote
  url: "http://pubsub-service:8080/sse"

beef:
  mode: remote
  url: "http://beef-service:8080"

txo:
  mode: remote
  url: "http://txo-service:8080"

indexer:
  mode: remote
  url: "http://indexer-service:8080"

bsv21:
  mode: remote
  url: "http://bsv21-service:8080"

ordfs:
  mode: remote
  url: "http://ordfs-service:8080"
```

### Example 4: Hybrid (Some Local, Some Remote)

Run some services locally, connect to others remotely.

```yaml
server:
  port: 8080

# Local infrastructure
store:
  mode: embedded
  url: "badger://~/.1sat-stack/store"

pubsub:
  mode: embedded
  url: "channels://"
  routes:
    enabled: true  # Serve SSE for local subscribers

# Local BEEF storage, serve routes
beef:
  mode: embedded
  chain: ["lru://?size=100mb", "~/.1sat-stack/beef"]
  routes:
    enabled: true

# Local TXO, serve routes
txo:
  mode: embedded
  routes:
    enabled: true

# Connect to remote indexer (heavy processing elsewhere)
indexer:
  mode: remote
  url: "http://indexer-cluster:8080"

# Connect to remote BSV21 service
bsv21:
  mode: remote
  url: "http://bsv21-service:8080"

# Local ORDFS for content serving
ordfs:
  mode: embedded
  routes:
    enabled: true
    prefix: "/content"  # Custom route prefix
```

### Example 5: Dedicated Indexer Node

Only runs the indexer, exposes its API.

```yaml
server:
  port: 8080

store:
  mode: embedded
  url: "badger://~/.indexer/store"

pubsub:
  mode: embedded
  url: "redis://redis:6379"  # Redis for multi-instance
  routes:
    enabled: false  # Don't serve SSE here

beef:
  mode: embedded
  chain: ["junglebus://"]  # Fetch from JungleBus only
  routes:
    enabled: false  # No BEEF API

txo:
  mode: embedded
  routes:
    enabled: true  # Serve TXO queries

indexer:
  mode: embedded
  indexers: [p2pkh, lock, inscription, ordlock, bsv20, bsv21, origin]
  routes:
    enabled: true  # Serve indexer API

# bsv21 and ordfs: omitted = disabled
```

---

## Implementation Phases

### Phase 1: Foundation

1. Copy `../overlay/` → `pkg/` (store, beef, pubsub, txo, sync, topics)
2. Update package paths from `github.com/b-open-io/overlay` to `github.com/b-open-io/1sat-stack/pkg`
3. Create `cmd/server/config.go` with Config struct, SetDefaults, and Load()
4. Create `cmd/server/main.go` with basic startup flow
5. Test that infrastructure compiles and runs

### Phase 1b: New Services

1. Create `pkg/merkle/service.go` - MerkleService
   - Subscribe to Arcade tx status events (via Arcade's subscription mechanism)
   - Subscribe to chaintracks block notifications
   - Implement score updates on outputs + events
   - Implement merkle state transitions
   - Implement rollback for invalidated outputs
2. Create `pkg/worker/worker.go` - Generic worker
   - Configurable concurrency
   - Catchup on startup
   - Error handling callbacks
3. Implement `ReconcileValidatedMerkleRoots` (currently a no-op in overlay)

### Phase 2: Indexer Package

1. Copy from `../1sat-indexer/`:
   - `idx/` → `pkg/indexer/idx/` (Indexer interface, scoring, IngestTx)
   - `mod/` → `pkg/indexer/mod/` (protocol parsers)
   - `server/routes/` → `pkg/indexer/routes/fiber/`
   - `sub/` → `pkg/indexer/subscriber/` (JungleBus subscription)
2. **Do NOT copy `ingest/`** - replaced by:
   - `pkg/worker/` for processing
   - `pkg/merkle/` for proof management
3. Create `pkg/indexer/config/` with Config, SetDefaults, Initialize, Services
4. Update imports to use `pkg/` packages
5. Wire up: JungleBus subscriber → store → worker → IngestTx
6. Test standalone with only indexer enabled

### Phase 3: BSV21 Package

1. Copy from `../bsv21-overlay/`:
   - `lookups/` → `pkg/bsv21/lookups/`
   - `topics/` → `pkg/bsv21/topics/`
   - `routes/` → `pkg/bsv21/routes/fiber/`
   - `peer/` → `pkg/bsv21/peer/`
2. Create `pkg/bsv21/config/` with Config, SetDefaults, Initialize, Services
3. Update imports to use `pkg/` packages
4. Test standalone with only bsv21 enabled

### Phase 4: ORDFS Package

1. Copy from `../go-ordfs-server/`:
   - `api/` → `pkg/ordfs/routes/fiber/`
   - `handlers/`, `loader/`, `cache/`, `ordfs/` → `pkg/ordfs/`
2. Create `pkg/ordfs/config/` with Config, SetDefaults, Initialize, Services
3. Update imports to use `pkg/` packages
4. Test standalone with only ordfs enabled

### Phase 5: Integration Testing

1. Test with all packages enabled together
2. Verify shared dependencies work correctly (BEEF, PubSub, SSE)
3. Build and run complete stack

---

## Critical Source Files

### NEW (to create)

| File | Description |
|------|-------------|
| `pkg/merkle/service.go` | MerkleService - proof management, score updates |
| `pkg/merkle/routes.go` | ARC callback route |
| `pkg/merkle/config.go` | Config + SetDefaults |
| `pkg/txo/routes.go` | TXO query routes (outpoint, search, etc.) |
| `pkg/beef/routes.go` | Raw tx data routes (already exists in overlay, may need refactor) |
| `pkg/worker/worker.go` | Generic worker pattern |
| `pkg/worker/config.go` | Config + SetDefaults |
| `cmd/server/main.go` | Entry point |
| `cmd/server/config.go` | Server config + Load() |

### COPY (from existing projects)

| Source | Destination | Notes |
|--------|-------------|-------|
| `../overlay/*` | `pkg/*` | Infrastructure packages |
| `../overlay/storage/output_store.go` | `pkg/txo/store.go` | Rename events → txo |
| `../overlay/routes/tx.go` | `pkg/beef/routes.go` | Raw tx data routes |
| `../overlay/routes/common.go` | `pkg/txo/routes.go` | Event routes → TXO search routes |
| `../1sat-indexer/idx/*` | `pkg/indexer/idx/*` | Indexer interface, scoring |
| `../1sat-indexer/mod/*` | `pkg/indexer/mod/*` | Protocol parsers |
| `../1sat-indexer/sub/*` | `pkg/indexer/subscriber/*` | JungleBus subscription |
| `../1sat-indexer/server/routes/tx/ctrl.go` | `pkg/indexer/routes/` | ParseTx, IngestTx only |
| `../1sat-indexer/server/routes/txos/*` | (merge into pkg/txo) | Direct outpoint lookups |
| `../1sat-indexer/server/routes/own/*` | `pkg/indexer/routes/` | Owner queries (indexer-specific) |
| `../1sat-indexer/server/routes/tag/*` | (merge into pkg/txo) | Tag queries → search routes |
| `../1sat-indexer/server/routes/evt/*` | (merge into pkg/txo) | Event queries → search routes |
| `../bsv21-overlay/lookups/*` | `pkg/bsv21/lookups/*` | LookupService |
| `../bsv21-overlay/topics/*` | `pkg/bsv21/topics/*` | TopicManager |
| `../bsv21-overlay/routes/*` | `pkg/bsv21/routes/fiber/*` | BSV21 routes |
| `../bsv21-overlay/peer/*` | `pkg/bsv21/peer/*` | Peer config |
| `../go-ordfs-server/api/*` | `pkg/ordfs/routes/fiber/*` | Content routes |
| `../go-ordfs-server/handlers/*` | `pkg/ordfs/handlers/*` | Request handlers |
| `../go-ordfs-server/loader/*` | `pkg/ordfs/loader/*` | Content loading |
| `../go-ordfs-server/cache/*` | `pkg/ordfs/cache/*` | Caching |
| `../go-ordfs-server/ordfs/*` | `pkg/ordfs/ordfs/*` | Core ORDFS logic |

### DO NOT COPY

| Source | Reason |
|--------|--------|
| `../1sat-indexer/ingest/*` | Replaced by `pkg/worker/` + `pkg/merkle/` |
| `../1sat-indexer/server/routes/tx/ctrl.go:TxCallback` | Moves to `pkg/merkle/routes.go` |
| `../1sat-indexer/server/routes/tx/ctrl.go:TxosByTxid` | Moves to `pkg/txo/routes.go` |
| `../1sat-indexer/config/*` | Create fresh package config |
| `../bsv21-overlay/config/*` | Create fresh package config |
| `../go-ordfs-server/config/*` | Create fresh package config |
| `../overlay/routes/common.go` (block routes) | Use chaintracks instead |

---

## Route Structure

Routes are defined without prefixes - consumers decide mount points.

### `pkg/beef/routes` (raw tx data from BEEF storage)

```
GET  /:txid              → Raw transaction bytes
GET  /:txid/proof        → Merkle proof bytes
GET  /:txid/beef         → BEEF format
GET  /:txid/:outputIndex → Raw output bytes
```

### `pkg/merkle/routes` (proof management)

```
POST /callback           → ARC callback (tx status changes, new proofs)
```

### `pkg/txo/routes` (indexed outputs)

```
# Direct lookups by outpoint
GET  /:outpoint          → Single indexed output
GET  /:outpoint/spend    → Spend info for outpoint
POST /                   → Batch outputs by outpoints (body: string[])
POST /spends             → Batch spend info (body: string[])

# By transaction
GET  /tx/:txid           → All outputs for a transaction

# Search by indexed key
GET  /search/:key        → Search outputs by key (?unspent=bool)
POST /search             → Batch search by keys (body: string[], ?unspent=bool)
```

### `pkg/indexer/routes` (indexer-specific)

```
GET  /:txid/parse        → Parse tx by txid, return indexed data
POST /parse              → Parse posted tx bytes
POST /:txid/ingest       → Force ingest tx by txid
```

### `pkg/routes` (common)

```
POST /submit             → BEEF submit with topics
GET  /sse/subscribe      → SSE subscription
POST /sse/unsubscribe    → SSE unsubscribe
```

### Example Mount Points (main.go)

```go
beefRoutes.Register(app.Group("/tx"))        // /tx/:txid, /tx/:txid/beef, etc.
merkleRoutes.Register(app.Group("/arc"))     // /arc/callback
txoRoutes.Register(app.Group("/txo"))        // /txo/:outpoint, /txo/search/:key, etc.
sseRoutes.Register(app.Group("/sse"))        // /sse/subscribe
submitRoutes.Register(app.Group("/api/v1"))  // /api/v1/submit

indexerRoutes.Register(app.Group("/idx"))    // /idx/:txid/parse, /idx/parse
bsv21Routes.Register(app.Group("/bsv21"))    // /bsv21/*
ordfsRoutes.Register(app.Group("/ordfs"))    // /ordfs/*
ordfsRoutes.RegisterContent(app)             // /content/* (root level)
```

---

## Swagger Documentation

All HTTP handlers **must** include Swag annotations for automatic OpenAPI documentation generation.

### Annotation Pattern

```go
// @Summary Get transaction output
// @Description Get a transaction output by outpoint
// @Tags txos
// @Accept json
// @Produce json
// @Param outpoint path string true "Transaction outpoint (txid_vout)"
// @Param tags query string false "Comma-separated list of tags"
// @Success 200 {object} idx.Txo
// @Failure 404 {string} string "TXO not found"
// @Failure 500 {string} string "Internal server error"
// @Router /txo/{outpoint} [get]
func GetTxo(c *fiber.Ctx) error {
    // ...
}
```

### Build Script

Create `build-docs.sh`:

```bash
#!/bin/bash
set -e

SWAG="$(go env GOPATH)/bin/swag"

# Check if swag is installed, install if not
if [ ! -f "$SWAG" ]; then
    echo "swag not found, installing..."
    go install github.com/swaggo/swag/cmd/swag@latest
fi

echo "Generating Swagger documentation..."
$SWAG init -g cmd/server/main.go -o docs --parseDependencyLevel 3 --parseGoList

echo "✓ Swagger documentation generated successfully"
echo "  - docs/swagger.json"
echo "  - docs/swagger.yaml"
echo "  - docs/docs.go"
```

### Required Annotations

| Annotation | Required | Description |
|------------|----------|-------------|
| `@Summary` | Yes | Brief one-line description |
| `@Description` | Yes | Detailed description |
| `@Tags` | Yes | Grouping for documentation |
| `@Accept` | If body | Content types accepted (json, octet-stream) |
| `@Produce` | Yes | Content types produced |
| `@Param` | If params | Path, query, or body parameters |
| `@Success` | Yes | Success response code and type |
| `@Failure` | Yes | Error response codes |
| `@Router` | Yes | Route path and HTTP method |

---

## Initialization Order

1. Load config (Viper: YAML + env vars)
2. Initialize enabled packages in dependency order (all optional, config-driven):
   - Logger
   - **Shared external services:**
     - P2P (if enabled) - from go-teranode-p2p-client
     - Arcade (if enabled, uses P2P)
     - Chaintracks (if enabled, uses P2P)
   - **Internal packages:**
     - Store (if enabled)
     - PubSub (if enabled)
     - BeefStorage (if enabled, requires Store)
     - TxoStore (if enabled, requires Store)
     - SSEManager (if enabled, requires PubSub)
     - MerkleService (if enabled, requires Store, PubSub, Chaintracks)
     - Indexer (if enabled, requires Store, BeefStorage)
     - BSV21 (if enabled, requires Store, PubSub)
     - ORDFS (if enabled, requires BeefStorage)
3. Register routes for initialized packages
4. Start services
5. Start HTTP server
