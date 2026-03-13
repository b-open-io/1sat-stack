# Event-Driven Overlay Routing

Status: **Not Started**

## Problem

Transactions enter the system through multiple paths — Arcade broadcasts, owner sync, overlay GASP, JungleBus subscriptions. Each overlay module currently relies on its own JungleBus subscription to discover relevant transactions. If a transaction arrives through any other path (e.g., a user broadcasts via Arcade), the overlay won't see it unless the same transaction also appears in JungleBus later.

## Solution

After the indexer parses a transaction (`IngestTx` → `SaveTransaction`), publish txid-level events to PubSub based on the parsed tags. Each overlay module subscribes to the events it cares about and enqueues the txid into its own processing queue. The existing overlay sync worker processes it normally.

## Components

### 1. PubSub Pattern Matching

Add glob-style pattern matching to `Subscribe`. Patterns like `bsv21:*` match all BSV21 token events. Patterns without wildcards behave as exact matches (backwards compatible).

**Implementation**: Convert glob patterns to compiled regexes on subscription. On `Publish`, check exact subscribers (existing map lookup) then iterate pattern subscribers (small fixed set — one per overlay module). When Redis PubSub is implemented, this maps to `PSUBSCRIBE`.

**Files**: `pkg/pubsub/pubsub.go` (interface unchanged), `pkg/pubsub/channels.go` (add pattern matching)

### 2. Txid-Level Event Publishing

In `SaveTransaction`, after all outputs and spends are saved, collect unique routing events and publish each once with the txid hex as the message.

**Prerequisite**: BEEF must be stored before `SaveTransaction` runs. The current flow guarantees this — Arcade stores BEEF before triggering indexing, JungleBus stores BEEF before enqueuing, and owner sync stores BEEF on arrival.

**Output events**: Derived from parsed tags. Each tag that has routing significance emits one event per unique value:
- `ordlock` → `"ordlock"` (exact match)
- `bsv21:{tokenId}` → `"bsv21:{tokenId}"` (one per token)
- `bap:{operation}` → `"bap:ID"`, `"bap:ATTEST"`, `"bap:REVOKE"`
- `map:type:{type}` → `"map:type:post"`, `"map:type:like"`, etc. (emitted by ParseMAP)
- `opns:mine` → `"opns:mine"` (exact match)

**Spend events**: For each spent input that was parsed with a routing tag, publish `"spend:ordlock"` with the spending txid. Initially only OrdLock spends matter.

**Replaces outpoint-level publishing**. The existing per-outpoint `Publish` calls in `SaveOutput` are removed — nothing subscribes to them. All PubSub publishing moves to `SaveTransaction` at the txid level.

**Deduplication**: A transaction arriving through multiple paths (e.g., Arcade + JungleBus) may produce duplicate txid events. The overlay engine's `Submit` is idempotent — processing the same txid twice is harmless. No dedup needed in the EventBridge.

**Files**: `pkg/txo/output_store.go` (add txid-level publish in `SaveTransaction`)

### 3. New/Modified Parsers

All new parsers follow the existing `pkg/parse` pattern: check preconditions, return nil if not applicable, emit events and data.

**ParseOPNS** (new): Detects OPNS mine outputs via `opns.Decode()` on the locking script. Emits `"opns:mine"`. Added to `DefaultTags` and `Parsers` map in `pkg/parse/parse.go`. Independent of other parsers — no ordering constraints.

**ParseMAP** (modified): Simplify to only emit `"map:type:{type}"`. Drop `"map:app:{appName}"` — nothing routes on app name. BSocial overlay subscribes to the MAP type events it cares about directly.

**ParseBAP** (modified): Add events. Emits `"bap:{operation}"` based on decoded BAP operation type.

**ParseOrdLock** (modified): Simplify event from `"ordlock:list"` to `"ordlock"`.

**Parse pipeline order**: `bitcom` → `map`, `bap`. `opns` and `ordlock` are independent.

**Files**: `pkg/parse/opns.go` (new), `pkg/parse/bitcom.go` (modify ParseMAP, ParseBAP), `pkg/parse/ordlock.go` (simplify event)

### 4. EventBridge (Shared Helper)

Reusable component that subscribes to PubSub patterns and enqueues txids into a store queue.

```go
type EventBridgeConfig struct {
    PubSub    pubsub.PubSub
    Store     store.Store
    Patterns  []string
    QueueFunc func(pubsub.Event) string  // returns queue key, empty to skip
    Logger    *slog.Logger
}
```

Single goroutine: read event from channel → call QueueFunc → ZAdd txid with timestamp score. No debounce needed since events are txid-level (one per tag per transaction).

**BSV21 note**: The BSV21 `QueueFunc` must parse the tokenId from the event topic (e.g., `"bsv21:{tokenId}"` → queue key `"q:tm_{tokenId}"`). Other modules use fixed queue keys.

**Files**: `pkg/overlay/event_bridge.go` (new)

### 5. Module Initialization Changes

For each overlay module, when enabled:

1. **Always start the sync worker** — regardless of whether JungleBus sync is configured. The worker polls its queue and processes whatever is there.
2. **Create EventBridge** — subscribes to relevant patterns, routes to module's queue.
3. **JungleBus subscriber** — only starts if `sync.enabled: true` and subscription ID is set. Optional additional source for the same queue.

This means `sync.enabled` controls JungleBus, not the worker. A module with no JungleBus subscription still processes transactions routed via PubSub.

**OPNS note**: OPNS currently has no OverlaySync worker — only a genesis crawl. An OverlaySync worker must be created for OPNS as part of this work, following the same pattern as BAP/BSocial/OrdLock.

**Files**: `cmd/server/config.go`, per-module config files

### 6. Routing Table

| Module | Subscribe Patterns | Queue Key | Notes |
|--------|-------------------|-----------|-------|
| OrdLock | `ordlock`, `spend:ordlock` | `q:ordlock` | Exact match — no wildcard needed |
| BSV21 | `bsv21:*` | `q:tm_{tokenId}` | QueueFunc parses tokenId from event |
| BAP | `bap:*` | `q:bap` | |
| BSocial | `map:type:*` | `q:bsocial` | QueueFunc filters for relevant types |
| OPNS | `opns:mine` | `q:opns` | Exact match — no wildcard needed |

## Data Flow

```
Transaction arrives (Arcade, owner sync, GASP, JungleBus)
    ↓
IngestTx → Parse (outputs + spends)
    ↓
SaveTransaction
    ├─ SaveOutput per output (store to Badger)
    ├─ SaveSpend per spend (store to Badger)
    └─ Publish txid-level routing events to PubSub
         ├─ "ordlock" + txid
         ├─ "bsv21:{tokenId}" + txid
         ├─ "spend:ordlock" + spending txid
         └─ etc.
              ↓
EventBridge (per module)
    ├─ Subscribes to patterns (exact or glob)
    ├─ QueueFunc determines queue key
    └─ ZAdd txid to queue with timestamp score
              ↓
OverlaySync worker (per module)
    ├─ Polls queue (always running if module enabled)
    ├─ Loads BEEF, submits to overlay engine
    └─ Engine calls topic manager → lookup service
```

## Queue Scoring

- **JungleBus items**: HeightScore (block height + block index) — historical ordering
- **Event-routed items**: `float64(time.Now().UnixMicro())` — sorts after all historical items, preserves arrival order at microsecond resolution

## What This Does NOT Change

- Overlay engine internals (Submit, GASP, topic managers, lookup services)
- Spend index storage (still outpoint-centric in `ev:spent:` keys)
- JungleBus subscription mechanics
- Queue worker processing logic
