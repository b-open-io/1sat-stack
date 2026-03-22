# Distributed Compute Vision

Status: **Aspirational**

## Core Idea

Functional modules across the entire OneSat ecosystem become distributable, runtime-loadable units of execution. Code is immutable and on-chain. State is passed in, not bundled. Any host with the right data can execute any module and get deterministic results.

This is not limited to server-side 1sat-stack. The same WASM modules run on servers, in browsers, in CLI tools, in agent runtimes — wherever there's a conforming host. A parser module inscribed on-chain works identically whether called by a 1sat-stack node indexing blocks, a wallet client parsing a transaction, or an agent executing an action.

## The Unification Opportunity

Today, the same protocol logic is implemented separately in multiple languages:

### Duplicated Implementations

| Logic | Go (server) | TypeScript (client/SDK) | Notes |
|-------|------------|------------------------|-------|
| **Script templates** (Inscription, BSV20, BSV21, OrdLock, Lock, MAP, AIP, BAP, Sigma, BSocial, OPNS, Cosign) | `go-templates/template/` | `ts-templates/src/template/` + `1sat-sdk/packages/templates/` | Encode + decode in both. Most direct duplication. |
| **Script parsing** (decode mined outputs) | `1sat-stack/pkg/parse/` (15 parsers) | `1sat-sdk/packages/wallet/src/indexers/` (10 indexers) | Server parsers feed overlays; client indexers feed wallet. Same decode logic. |
| **Protocol constants** (prefixes, suffixes, markers) | Scattered across Go packages | `@1sat/types` constants | Must stay in sync manually. |
| **BEEF handling** (assembly, merkle proofs) | `1sat-stack/pkg/beef/` | `@1sat/actions` (completeSignedAction, resolveBeef) | Transaction proof chain logic. |
| **Action patterns** (two-phase signing, tracked outputs) | Go wallet operations | `@1sat/actions` (Action interface, createTrackedAction) | BRC-100 action lifecycle. |

With WASM modules, these converge. One implementation per protocol, usable everywhere.

### Where Modules Run

| Host Environment | Runtime | Examples |
|-----------------|---------|---------|
| **1sat-stack server** | Wazero (Go) | Indexing, overlay admission, lookups, API serving |
| **Browser wallet** | Native WASM | Parsing received transactions, validating tokens, building scripts |
| **CLI tools** | Wazero or native WASM | Offline transaction building, local validation |
| **Agent runtimes** | Any WASM host | Executing actions, querying overlays |
| **Third-party apps** | Any WASM host | Protocol integration without language-specific SDK dependency |

The host provides environment-specific capabilities (storage, networking, signing) through host functions. The module provides protocol logic. Same module binary everywhere.

## Module Candidates

The full 1sat-stack has distinct functional areas, each with different distribution characteristics:

### Already Well-Interfaced (Lowest effort)

| Area | Current Interfaces | Distribution Notes |
|------|-------------------|-------------------|
| **Overlay protocols** (BAP, BSV21, OPNS, OrdLock, BSocial) | `engine.TopicManager`, `engine.LookupService` | Two clean interfaces per protocol. Closest to ready. |
| **BEEF storage** | `BaseBeefStorage` (Get, Put, UpdateMerklePath, GetRawTx, GetProof) | Multiple backends already (Redis, Badger, FS, JungleBus). Interface is narrow. |
| **PubSub** | `PubSub` (Publish, Subscribe, Unsubscribe) | Clean interface, multiple implementations. Host concern, not a module. |
| **Store** | `Store` — Redis-like KV/Set/Hash/SortedSet ops | Foundation layer. This IS the host function surface for data access. |
| **ORDFS** | `OriginStore`, `Cache`, `CrawlCoordinator` | Well-separated interfaces. Content resolution logic is the module; storage is the host. |

### Modular But With Coupling (Medium effort)

| Area | Current State | What Needs Work |
|------|--------------|-----------------|
| **Script parsers** (`pkg/parse/`) | 15 stateless parsers (p2pkh, inscription, bsv21, ordlock, etc.) run in order via tag system | Already stateless and ordered. Need a standard parser interface for WASM loading. Currently just functions, not an interface. |
| **Indexer pipeline** (`pkg/indexer/`) | Drives parse → ingest flow. Injected OutputStore, BeefStore. | Orchestration logic (host concern). The parsers it calls are the modules. |
| **Queue worker** (`pkg/worker/`) | Generic sorted-set queue processor with configurable handler | Host infrastructure. The handler function is what becomes a module. |
| **Owner sync** (`pkg/owner/`) | JungleBus address sync with deduplication | Mixed: sync coordination is host, but address discovery logic could be modular. |
| **Merkle service** (`pkg/merkle/`) | Proof validation and score updates | Tightly coupled to overlay storage adapter. Needs decoupling to be host-only. |

### Tightly Coupled (Higher effort)

| Area | Current State | What Needs Work |
|------|--------------|-----------------|
| **REST routes** (per-module `routes.go`) | Call concrete LookupService methods directly, bypass `Lookup()` interface | Refactor to go through `LookupQuestion`/`LookupAnswer`. Then API modules become distributable too. |
| **BSV21 fee/payment logic** (`pkg/bsv21/`) | Protocol-specific fee and payment calculation | Stays as host logic. The TokenManager is just dynamic topic activation — deploy overlay discovers token IDs, host spins up per-token engines with the standard BSV21 validation module. Per-token overlays are standard `TopicManager` + `LookupService`. |
| **Paymail** (`pkg/paymail/`) | Chains OPNS lookup → ORDFS MAP resolution → BRC-29 derivation | Cross-module dependency chain. Could be a module that calls other modules through host functions. |
| **Auth/Admin** (`pkg/auth/`) | BRC-103/104 verification, Sigma auth, session management | Security boundary. Stays as host infrastructure, never a WASM module. |
| **Wallet** (`pkg/wallet/`) | BRC-100 wallet with GORM DB, fee model, Chaintracks, Arcade | Complex external dependencies. Likely stays as host service. |

## Architecture Layers

### Module Types

1. **Template module** — script encoding and decoding for a protocol (lock, unlock, decode). The atomic unit of protocol knowledge. Used by both parsers (decode) and actions (encode). Today split across `go-templates/`, `ts-templates/`, `1sat-sdk/packages/templates/`.
2. **Parser module** — uses template decode to extract structured data from mined outputs. Today: `1sat-stack/pkg/parse/` (server) and `1sat-sdk/packages/wallet/src/indexers/` (client). Same WASM module serves both.
3. **Engine module** — implements `TopicManager` + `LookupService` (admission, indexing, querying). Overlay protocol logic.
4. **Action module** — self-describing operations (send, mint, list, transfer). Uses templates to build transactions. Today: `1sat-sdk/packages/actions/`. Could run on any host with wallet + signing capabilities.
5. **API module** — maps protocol-specific query shapes to/from `LookupQuestion`/`LookupAnswer`.
6. **Resolution module** — content resolution logic (ORDFS crawling, origin tracking).

All distributable. All loadable at runtime. All behind standard interfaces. All inscribable on-chain.

### The Server Is Wiring

The 1sat-stack server reduces to:

1. **Boot** — create storage backends (Badger instances, SQLite files), start redcon listeners
2. **Wire** — for each module, create the right set of named channels scoped to what that module needs
3. **Run** — modules work through their channels. Host bridges between modules and external interfaces.

The entire dependency injection surface becomes: **which channels does this module get**. No custom interfaces, no wrapper types, no facade layers. Configuration becomes: what channels exist, what backends they point at, which modules access which channels.

Channel scopes:

| Scope | Example | Purpose |
|-------|---------|---------|
| **Shared** | `txo`, `progress` | Cross-module state (output metadata, sync progress) |
| **Module-scoped** | `beef`, `bap_db` | Isolated to one module |
| **Topic-scoped** | `q:tm_bap`, `bsv21:{tokenId}` | Per-overlay-topic storage |

### PubSub Collapses Into Redis

PubSub is a Redis primitive (`PUBLISH`/`SUBSCRIBE`). Modules that need to publish or subscribe to events do so through their Redis channel — no separate PubSub interface needed.

The host bridges Redis PubSub to external transports:
- **SSE** — host subscribes internally, streams to HTTP clients
- **P2P** — host subscribes internally, broadcasts via libp2p
- **WebSocket** — host subscribes internally, forwards to WS clients

Modules don't know or care about the external transport. They `PUBLISH` an event; the host routes it.

### Host Responsibilities (Never modules)

- WASM runtime (Wazero) — executes modules in sandbox
- Channel registry — manages named channels (Redis + SQLite), controls access per module
- Channel provisioning — creates per-consumer streams for each module's declared dependencies
- P2P transport — libp2p messaging, GASP coordination (bridges to Redis PubSub)
- Auth/session management — security boundary
- Wallet services — key management, signing, broadcasting
- HTTP server scaffold — routes requests to API modules, bridges SSE from Redis PubSub
- Module resolution — fetches modules from ORDFS by outpoint + content type
- Data resolution — serves content-addressable lookups, locally or via peer negotiation

### Module Contract — Two Channel Types

Modules access data through two complementary channel types. Both are deterministic, well-specified, universally known, and compile to WASM. A module declares which channel types it needs; the host provides them backed by whatever.

#### Redis Channel — Operational Data

For KV, hashes, sorted sets, pub/sub. The hot-path operational interface.

- **Semantics**: Redis command set (GET, SET, HSET, ZADD, PUBLISH, etc.)
- **Use cases**: Queues, caches, events, topic state, configuration, pub/sub
- **Backend**: Badger (via redcon), real Redis, on-chain state, IndexedDB
- **Frontend**: go-redis, ioredis, redis-py, redis-cli, or any RESP client

#### SQLite Channel — Structured Data

For indexed queries, aggregates, joins, text search. The analytical/relational interface.

- **Semantics**: SQLite SQL dialect (precisely defined, deterministic)
- **Use cases**: Identity lookups (BAP), token balances (BSV21), text search, multi-field queries
- **Backend**: SQLite file, in-memory SQLite, or a Postgres adapter that accepts SQLite-dialect SQL
- **Frontend**: Any SQLite client library, or raw SQL over the channel
- **WASM**: SQLite compiles to WASM natively (sql.js, official WASM build) — modules can carry their own instance in browser environments

The two interfaces are complementary, not overlapping. Redis for operational state, SQLite for structured queries. Both deterministic, both portable.

#### Channel Model

A module doesn't receive a connection address or a typed client object. It receives **named channels** — bidirectional byte streams. The host provides the transport; the module provides the protocol logic.

```
Module                          Host
  │                               │
  │  channel_request(channel_id,  │
  │    command_bytes)             │
  │  ─────────────────────────►   │
  │                               │  route to backend by channel type
  │                               │  (RESP or SQL) → execute → encode
  │  ◄─────────────────────────   │
  │  response_bytes               │
  │                               │
```

Each channel has a type (redis or sqlite) and a name. A module might receive:
- `txo` (redis) — operational output state
- `bap_db` (sqlite) — identity records with relational queries
- `topics` (redis) — topic manager state

The host function signature for WASM modules:

```
channel_request(channel_id, command_ptr, command_len) → (response_ptr, response_len)
```

For each environment:

| Environment | Redis Channel | SQLite Channel |
|-------------|--------------|----------------|
| **WASM in Wazero** | Host functions backed by Badger | Host functions backed by SQLite file |
| **In-process Go** | go-redis to embedded redcon | database/sql to SQLite |
| **Browser WASM** | Host functions backed by IndexedDB | sql.js (WASM SQLite) |
| **External process** | TCP to redcon RESP listener | SQLite wire protocol or HTTP |
| **Tooling** | redis-cli | sqlite3 CLI, DBeaver |

Every language's Redis client library needs only a custom connection adapter that routes to the host function instead of a TCP socket. The byte-level protocol is identical to what goes over the wire to a real Redis server.

#### Multiple Named Channels

A single 1sat-stack instance manages many separate logical databases. These are NOT intermixed — each has its own storage, its own namespace, its own backend. Each channel has a type.

| Channel | Type | Purpose | Backend |
|---------|------|---------|---------|
| `txo` | redis | Indexed output metadata, events, merkle state | Badger |
| `beef` | redis | Transaction proof storage | Filesystem + JungleBus fallback |
| `topics` | redis | Topic manager state per overlay | Badger |
| `config` | sqlite | Runtime application settings | SQLite |
| `wallet` | sqlite | Wallet state, keys, certificates | SQLite |
| `bap_db` | sqlite | BAP identities, addresses, attestations | SQLite |
| `bsv21:{tokenId}` | sqlite | Per-token output records, balances | SQLite (dynamic) |
| Per-token event state | redis | Queue state, scores, events | Badger (dynamic) |

A module receives channel handles to specific named channels. An engine module for BSV21 gets `txo` (redis) for event state and `bsv21:{tokenId}` (sqlite) for token queries, but not `wallet` or `config`. The host controls which channels a module can access — this is the security/isolation boundary.

Each channel is exclusive to its consumer. If two modules need the same underlying store, the host creates two separate channels that both route to the same backend. The backend handles concurrency. No two consumers share a byte stream.

#### Channel Types Per Overlay

| Overlay | Redis Channels | SQLite Channels | Notes |
|---------|---------------|-----------------|-------|
| **OPNS** | events, txo | — | Already event-based, clean Redis fit |
| **OrdLock** | events, txo | — | In-memory struct, minimal storage |
| **BAP** | events | bap_db | Identities, addresses, attestations need relational queries |
| **BSV21** | events, txo | bsv21:{tokenId} | Token balances, UTXO queries, aggregates |
| **BSocial** | events | bsocial_db | Posts, follows, full-text search |

#### Why These Two Interfaces

Evaluated: SQL, gRPC/Protobuf, S3 API, Arrow Flight, NATS KV, FoundationDB layers.

Redis command semantics hit a sweet spot for operational data. Multiple data structure types (KV, hashes, sorted sets, sets, pub/sub, atomic operations) under one protocol with well-understood behavior.

SQLite fills the gap Redis can't — structured queries, aggregates, joins, text search. Critically, SQLite is deterministic (same query + same data = same result, always). A Postgres adapter that accepts SQLite-dialect SQL gives flexibility at the backend without compromising the module contract.

Everything else evaluated was either too narrow (pure KV), too complex (full Postgres wire protocol), or too custom (inventing a query language).

#### Implementation: redcon + Badger

[redcon](https://github.com/tidwall/redcon) is a RESP protocol framework (not a server). It handles connection management and RESP parsing/writing. You implement all command handlers yourself.

**Key properties:**
- Zero built-in commands — complete control over semantics
- ServeMux for per-command handler registration
- ~2x Redis throughput on raw protocol benchmarks
- RESP2 (sufficient for our needs)
- 444 GitHub dependents, Tile38 uses it in production
- Built-in PubSub infrastructure

**What we build:**
- Command handlers for the Redis subset we use (GET, SET, DEL, HSET, HGET, ZADD, ZRANGE, SADD, SMEMBERS, etc.) backed by Badger
- Redcon exposes this over RESP for external tooling and any-language clients
- WASM host functions call the storage logic directly (no protocol overhead for in-process modules)
- Same storage layer, two access paths

**Proven pattern:** `zaibon/redis-badger` (archived, minimal) validates the combination. `nalgeon/redka` does redcon + SQLite/PostgreSQL with full data type support. `summitdb` (archived) had JSON commands (JSET/JGET/JDEL) on redcon.

**Redis JSON:** Not built into redcon. Could implement simplified JSON commands (like summitdb's JSET/JGET approach) as custom handlers if needed. Full RedisJSON JSONPath semantics would be significant work.

With this in place:

1. WASM modules issue Redis commands as host function calls (direct, no protocol overhead)
2. External consumers use any Redis client library in any language (via RESP)
3. The host resolves commands against the appropriate backend
4. External tooling (redis-cli, monitoring) works for free

#### Resolution Tiers

A module issues a Redis command. The host resolves it across tiers transparently:

| Tier | Backend | Characteristics |
|------|---------|----------------|
| **Local** | Badger (via redcon in-process) | Fastest. Node's own indexed state. |
| **Shared** | Redis/Kvrocks (network) | Shared state across processes on same infrastructure. |
| **Peer** | GASP / P2P request | Data not available locally, fetched from overlay peers. |
| **On-chain** | ORDFS (inscribed key-value pairs) | Immutable state. Reinscriptions = versioned state transitions. |

The module doesn't know which tier served the response. The command is the same regardless.

#### On-Chain State

Ordinal reinscriptions with attached key-value pairs give you on-chain state that speaks the same interface. A reinscription is a state transition — the key-value pairs at each inscription point are the state at that version. The host can serve these through the same Redis command surface, and you get an immutable audit trail of state changes for free.

#### Additional Host Functions

Beyond the Redis data surface, modules need a small set of non-data host functions:

| Host Function | Purpose | Used By |
|--------------|---------|---------|
| BEEF access | `GetRawTx`, `GetProof` | Parsers, validators |
| Chain state | Headers, height | Proof validation |
| Publish events | Admission notifications | Engine modules |
| Content resolve | ORDFS content loading | Origin tracking, MAP resolution |

### Distribution

- Modules inscribed as ordinals (WASM binary, content-typed)
- Resolvable via ORDFS by outpoint
- Content type determines runtime dispatch (`application/wasm`, future formats)
- Node operators opt into modules by referencing outpoints in config
- New protocol = inscribe module + any node can load it

### Runtime Flexibility

The host function interface is runtime-agnostic. Content type on the inscribed module determines which runtime handles it. Start with WASM, but the architecture supports adding runtimes (RISC-V, others) without changing the module contract.

Multiple source languages can target WASM: Go (via TinyGo), Rust, Zig, AssemblyScript.

## bsvz — The WASM Module Language

[bsvz](https://github.com/b-open-io/bsvz) is a comprehensive BSV foundation library written in Zig. It is the primary candidate for authoring WASM modules because:

- **Zig compiles to WASM natively** — `wasm32-freestanding` and `wasm32-wasi` are first-class build targets, no special toolchain needed
- **Small binaries** — no runtime, no GC, explicit allocator control. WASM output is dramatically smaller than Go→WASM or even Rust in many cases
- **Deterministic** — no hidden allocations, no undefined behavior, predictable execution
- **BSV primitives already implemented** — 27 BRC standards, full script interpreter (1,435/1,499 test vectors passing), transaction builder, BEEF v1/v2/Atomic, SPV verification, Merkle paths, Type-42 key derivation, BRC-77/78 messages

### What bsvz Already Has

| Area | Coverage |
|------|----------|
| Crypto | SHA256/512, RIPEMD160, HASH160/256, HMAC, secp256k1, ECDSA, Schnorr, AES-CBC/GCM, ECIES |
| Keys | EC keys, BIP32/BIP39 HD keys, Type-42 derivation (BRC-42/43), base58/WIF |
| Script | Full interpreter, parser, opcodes, builder, type detection, ASM encode/decode |
| Templates | P2PKH, OP_RETURN, PushDrop, R-puzzle (core templates — not protocol-specific) |
| Transactions | Parse/serialize (standard + extended format), sighash, builder, fee calculation |
| BEEF | v1/v2/Atomic parsing and serialization |
| SPV | MerklePath verification, BEEF verification |
| Messages | BRC-77 signed messages, BRC-78 encrypted messages |

### What bsvz Needs for Module Authoring

Protocol-specific templates and parsers — the encode/decode logic that today lives in `go-templates/` and `ts-templates/`:

| Protocol | Go | TypeScript | bsvz |
|----------|------|-----------|------|
| Inscription | ✅ `go-templates/template/inscription/` | ✅ `ts-templates/src/template/inscription/` | ❌ |
| BSV20/BSV21 | ✅ `go-templates/template/bsv20/`, `bsv21/` | ✅ `ts-templates/src/template/bsv20/`, `bsv21/` | ❌ |
| OrdLock | ✅ `go-templates/template/ordlock/` | ✅ `ts-templates/src/template/ordlock/` | ❌ |
| Lock (timelock) | ✅ `go-templates/template/lockup/` | ✅ `ts-templates/src/template/lockup/` | ❌ |
| BitCom (B, MAP, AIP, BAP, Sigma) | ✅ `go-templates/template/bitcom/` | ✅ `ts-templates/src/template/bitcom/` | ❌ |
| BSocial | ✅ `go-templates/template/bsocial/` | ✅ `ts-templates/src/template/bsocial/` | ❌ |
| OPNS | ✅ `go-templates/template/opns/` | ❌ | ❌ |
| Cosign | ✅ `go-templates/template/cosign/` | ❌ | ❌ |

Once these protocol templates are implemented in bsvz, the same WASM binary handles encode (for actions/transaction building) and decode (for parsing/indexing) across all host environments.

### WASM Build Path

No WASM target is configured in bsvz yet. Adding one is straightforward:

```zig
// in build.zig — add a WASM target
const wasm = b.addSharedLibrary(.{
    .name = "bsvz",
    .root_source_file = .{ .path = "src/root.zig" },
    .target = b.resolveTargetQuery(.{ .cpu_arch = .wasm32, .os_tag = .wasi }),
    .optimize = .ReleaseSmall,
});
```

Individual protocol modules would each produce their own small WASM binary with only the dependencies they need, keeping inscribed artifacts minimal.

## Data Layer Aspiration

All data access routes through Redis commands. Modules don't know or care where data lives. The host resolves commands across local storage, peers, and on-chain state transparently.

Reinscribed ordinals with key-value pairs make on-chain state a first-class participant in this model. A module's `GET` might return a value from Badger, from a peer via GASP, or from an inscribed state snapshot resolved through ORDFS. The command and the result shape are identical regardless of source.

This collapses the distinction between "local database," "peer state," and "on-chain state" into a single interface. The resolution tier is a host concern, invisible to modules.

## Current State Summary

### Strengths
- Explicit dependency injection throughout — no global state
- `Store` interface already mirrors Redis command semantics (KV, Set, Hash, SortedSet) — prerequisite for adopting Redis as the universal data interface
- Topic managers and lookup services implement standard interfaces
- BEEF, ORDFS, PubSub all behind clean interfaces
- Dynamic topic activation/deactivation at runtime
- Parsers are stateless functions with ordered execution
- Deduplication layer (Loader/Saver) handles concurrent access

### Known Gaps

Server-side:
- REST routes bypass `Lookup()` interface, calling concrete DB methods directly
- Script parsers are functions, not a formal interface — need a standard contract
- BSV21 fee/payment calculation is host logic; per-token overlays are already standard
- Paymail chains cross-module calls that would need host function mediation
- Merkle service coupled to overlay storage adapter
- No WASM runtime integration yet
- Module configuration assumes compile-time registration — needs runtime discovery

Cross-ecosystem:
- Templates implemented separately in Go (`go-templates/`) and TypeScript (`ts-templates/`, `@1sat/templates`) — no shared source of truth
- Parser logic duplicated between server (`pkg/parse/`) and client (`wallet/indexers/`) with divergent output shapes
- Protocol constants defined independently in Go and TypeScript — must stay in sync manually
- Action interface (`@1sat/actions`) is TypeScript-only; Go has no equivalent action abstraction
- bsvz has BSV primitives (crypto, script, tx, BEEF, SPV) but no protocol-specific templates yet — those need porting before WASM modules can replace Go/TS implementations
- bsvz has no WASM build target configured yet (straightforward to add)
- No standard module manifest format for on-chain distribution (what interfaces a module implements, version, dependencies)

## Decision Guidance

When making architectural decisions across the OneSat ecosystem, prefer choices that:

- Keep module interfaces standard and narrow
- Route data access through abstract key-value or `Lookup()` interfaces rather than direct DB access
- Avoid tight coupling between modules and specific storage backends
- Maintain the separation between orchestration (host) and logic (module)
- Keep parsers stateless — input in, structured data out
- Treat security (auth, sessions, keys) as host-only concerns
- Avoid adding new protocol logic in language-specific ways — consider whether it should be a portable module
- When implementing new templates or parsers, design them as pure functions with no environment dependencies
- Prefer the same data shapes for parsed output regardless of where parsing happens (server vs client)
