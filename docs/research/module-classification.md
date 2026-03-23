# Module Classification for WASM Portability

## Tier 1: Pure Functions → WASM First

No state, no I/O. Script bytes in, structured data out. Port to Zig immediately.

| Module | What it does | Inputs | Outputs | Dependencies |
|--------|-------------|--------|---------|-------------|
| `parse` | Protocol parsers (inscription, MAP, BAP, AIP, SIGMA, B, BSV21, Shrug) | script bytes | structured events | go-templates (also port to Zig) |
| `types` | Data types, PKHash, OrdinalOutput | raw bytes | typed structs | go-sdk/transaction |
| `p2p` | GASP envelope serialize/deserialize | binary frames | structured envelopes | encoding/binary |

These are the first Zig WASM modules. Each one proves more of the pipeline.

## Tier 2: Storage-Backed Logic → WASM with Redis Channel

Core logic is simple, but needs a Redis channel for state. Good WASM candidates.

| Module | What it does | Channel needs | Notes |
|--------|-------------|--------------|-------|
| `spends` | Spend tracking | HGet, HSet, HDel, HMGet | Pure get/set over channel. Multi-provider chain stays in host. |
| `txo` | Output metadata store | HSet, HGet, HGetAll, HDel, HMGet, ZAdd, ZRem, ZScore, Scan | Heaviest Redis user (19 calls). Core output store. |
| `ordfs` | Content resolution | Reads from beef store + spends | Could be WASM with channels to beef and spends backing stores |
| `dedup` | Deduplication cache | In-memory (sync.Map) | Trivial in any language |

## Tier 3: Overlay Topic Managers → WASM with Redis + SQLite

Implement TopicManager interface. Need Redis channel for engine storage, SQLite file for lookup data.

| Module | What it does | Redis needs | SQLite needs | Notes |
|--------|-------------|------------|-------------|-------|
| `opns` | OPNS name overlay | Engine output membership | Domain mappings | Event-shaped, minimal SQL |
| `ordlock` | OrdLock marketplace | Engine output membership | Listing data | Minimal storage |
| `bap` | BAP identity overlay | Engine output membership | 3 tables, joins, text search | Heavier SQL |
| `bsv21` | BSV21 token overlay | Engine output membership + queue (ZAdd, ZCard) | token_outputs, balances | Both channels active |
| `bsocial` | Social protocol overlay | Engine output membership | Currently MongoDB → needs migration | TBD |

These are the core overlay modules. Each one is a WASM module that receives:
- A Redis channel (engine storage, queue ops)
- A SQLite file (lookup tables)
- Protobuf function calls (identifyAdmissibleOutputs, lookup)

## Tier 4: Host Infrastructure → Stays in Go

Orchestration, networking, concurrency, multi-provider chains. No reason to port to WASM.

| Module | What it does | Why it stays in Go |
|--------|-------------|-------------------|
| `store` | Store interface + redcon server + Badger | IS the host's storage layer |
| `channel` | Mux + net.Conn adapter | IS the host's channel infrastructure |
| `config` | Config store (SQLite) | Host configuration, not a module |
| `logging` | Structured logging | Host concern |
| `pubsub` | Pub/sub abstraction | Host concern — bridges to external transports |
| `jbsync` | JungleBus subscriber | Network I/O, blockchain sync state |
| `worker` | Queue worker loop | Concurrency pattern, goroutines |
| `indexer` | Parse pipeline orchestrator | Coordinates parsers + storage. Could become WASM later if engine becomes WASM. |
| `merkle` | Merkle proof tracking | Chaintracks integration, callbacks |
| `gasp` | GASP dependency resolution | Overlay engine coordination |
| `owner` | Owner/address sync | JungleBus + TXO orchestration |
| `beef` | BEEF storage chain | Multi-provider (LRU, filesystem, JungleBus, Badger, Redis) |
| `overlay` | Overlay engine + sync | Orchestrates topic managers, manages storage. Targets WASM eventually. |

## Tier 5: Application Services → Stays in Go

HTTP servers, wallet operations, external APIs. These are the outermost shell.

| Module | What it does | Why it stays in Go |
|--------|-------------|-------------------|
| `paymail` | BRC-29 paymail | HTTP + OPNS + ORDFS coordination |
| `wallet` | BRC-100 wallet service | SQLite + Arcade + Chaintracks |
| `messagebox` | Message box service | SQLite + HTTP |
| `auth` | Admin auth middleware | HTTP middleware, Fiber-specific |
| `httputil` | HTTP cache helpers | Fiber-specific |
| `chaintracks` | Chain header service | External service wrapper |
| `arcade` | Transaction broadcast | External service wrapper |

## Dependency Graph (WASM modules only)

```
Tier 1 (pure functions, no deps):
  parse ──────────────────────┐
  types ──────────────────────┤
  p2p ────────────────────────┤
                              │
Tier 2 (Redis channel):      │
  spends ─── [redis] ────────┤
  txo ─────── [redis] ───────┤
  ordfs ───── [redis to beef/spends] ──┤
                              │
Tier 3 (Redis + SQLite):     │
  opns ────── [redis, sqlite] ┤
  ordlock ─── [redis, sqlite] ┤
  bap ─────── [redis, sqlite] ┤
  bsv21 ───── [redis, sqlite] ┤
                              │
  All topic managers depend on:
    - parse (for protocol parsing)
    - types (for data types)
    - Engine interface (host-mediated calls)
```

## Migration Sequence

1. **parse** — Pure functions. First Zig module. Proves the pipeline (Zig → WASM → Wazero → protobuf boundary → same output as Go parser).
2. **types** — Data types used by everything. Port alongside parse.
3. **spends** — Simplest Redis channel consumer. Proves channel wiring in WASM.
4. **opns** or **ordlock** — Simplest overlay. Proves Redis + SQLite + TopicManager interface in WASM.
5. **txo** — Heavy Redis user. Core output store.
6. **bap** / **bsv21** — Full overlays with complex SQL.
7. **overlay engine** — The engine itself as WASM. Last piece.
