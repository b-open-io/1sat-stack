# Overlay Sync Routing

How transactions reach overlay topic queues from broadcast and JungleBus.

## Two Ingestion Paths

Every overlay module receives transactions through two paths:

1. **JungleBus** (historical + mined): JungleBus subscriber writes txids to a queue. A worker drains the queue and submits to the overlay engine. Transactions arrive after mining (~10 minutes).

2. **Broadcast** (real-time): Arcade accepts a transaction, the indexer ingests it, parsers emit pubsub events, and an EventBridge routes those events to the overlay queue. Transactions arrive within seconds.

Both paths converge on the same per-module overlay queue. ZAdd is idempotent, so duplicate entries from both paths are harmless.

```
BROADCAST PATH                              JUNGLEBUS PATH
     │                                            │
Arcade.Submit()                         JungleBus Subscriber
     │                                            │
StatusHandler.handleAccepted()           q:{module} or q:bsv21
     │                                       (32-byte txids)
IngestCtx.IngestTx()                          │
     │                                   OverlaySync worker
OutputStore.SaveTransaction()            or BSV21 Dispatcher
     │                                        │
PubSub.Publish(event, outpoint)          engine.Submit()
     │
EventBridge (pattern match)
     │
q:{module} or q:tm_{tokenId}
  (36-byte outpoints)
     │
OverlaySync worker
     │
engine.Submit()
```

## Event Bridge

`pkg/overlay/event_bridge.go` subscribes to pubsub patterns and enqueues matching events into overlay queues.

Each module's bridge is wired up in `cmd/server/config.go` `StartSubscribers()`:

| Module | Patterns | Queue | Notes |
|--------|----------|-------|-------|
| BAP | `bap:*` | `q:bap` | Fixed queue |
| BSocial | `map:type:*` | `q:bsocial` | Fixed queue |
| OPNS | `opns:mine` | `q:opns` | Fixed queue |
| OrdLock v2 | `ordlock2`, `spend:ordlock2` | `q:ordlock2` | Includes spend events; topic `tm_ordlock_v2` |
| gib | — | — | **No bridge.** gib has no queue and ingests nothing from the feed; heads arrive only by direct submission. Its `gib` / `gib:{origin}` events are a reader's feed, and `gib.SpendSync` subscribes to `spend:gib` to close out heads whose branch was deleted |
| BSV21 | `bsv21:*` | `q:tm_{tokenId}` | Routes to per-token queues, bypasses dispatcher |

Events are published by `OutputStore.SaveTransaction()` (`pkg/txo/output_store.go:249-273`) after the indexer parses a transaction. Each parser attaches events to its `ParseResult.Events` field.

The event bridge converts outpoint strings to 36-byte binary members via `parseEventMember()`. The OverlaySync worker handles both 36-byte (outpoint) and 32-byte (txid) members.

## Module Strategies

### Simple Modules: BAP, BSocial, OPNS, OrdLock

These use `overlay.OverlaySync` — the generic sync worker. Key settings:

- **ResolveDependencies: false** — no GASP. Uses `processDirect`: loads full BEEF, calls `engine.Submit()` directly.
- **Single topic queue** — one queue per module (e.g., `q:ordlock2`).
- **JungleBus subscriber optional** — can operate solely from the indexer's JungleBus subscription via the event bridge path. If a module-specific JungleBus subscription ID is configured, it provides a dedicated feed.

The topic managers for these modules don't require inputs to be pre-existing in the overlay. OrdLock v2's `IdentifyAdmissibleOutputs` checks if the output matches the compiled v2 template — it doesn't verify input balances. This is why `processDirect` (no GASP) works.

### gib: Submission Only

gib is the exception, deliberately. It has **no queue, no event bridge and no
sync worker**: a head enters `tm_gib` only when a client submits it through
the overlay's BRC-22 route, together with the content transactions that prove
the push it publishes (see `pkg/gib/README.md`). `processDirect` could not
serve it anyway — it builds the submission from the head transaction's own
ancestry, and a push's content transactions are not ancestors of the head.

gib is a far more explicit push than anything else here: the client holds the
repository, so the overlay never has to discover one. Repository exchange
between overlays is peer to peer, not a chain feed.

`gib.SpendSync` still subscribes to the `spend:gib` event so a branch
deletion (a spend with no successor head) closes out a head the index
already holds. It submits nothing to the engine and admits nothing.

### BSV21: Per-Token Queues

BSV21 uses a custom `SyncServices` (`pkg/bsv21/sync.go`) with:

- **Dedicated JungleBus subscriber** — feeds `q:bsv21` with 32-byte txids.
- **Dispatcher** — reads `q:bsv21`, parses BSV21 outputs, routes to per-token queues (`q:tm_{tokenId}`) as 36-byte outpoints. Also handles deploy submissions to the `tm_bsv21` discovery topic.
- **Token Manager** — manages worker lifecycle for active tokens. Each active token gets its own `OverlaySync` worker with `ResolveDependencies: true` (GASP enabled).
- **GASP dependency resolution** — BSV21's topic manager requires `previousCoins` to verify token balance (`tokensIn >= tokensOut`). Inputs must already exist in the overlay before a transfer can be admitted.

The event bridge routes `bsv21:*` events directly to per-token queues, bypassing the dispatcher. The dispatcher's deploy-handling isn't needed on the broadcast path since deploys are one-time events.

## Why OrdLock Uses the Indexer Route

OrdLock v2's JungleBus subscription (filtered on the junglebus `ordlock2` output/input type) feeds the general-purpose indexer queue rather than having a completely independent path. This works because:

1. **No balance validation** — OrdLock v2 admission doesn't check input balances. Any 1-sat output matching the v2 template is admitted. No GASP needed.
2. **processDirect is sufficient** — the full BEEF is loaded from beef storage and submitted to the engine. The topic manager validates the script pattern directly from the BEEF.
3. **Spend tracking via events** — OrdLock v2 subscribes to both `ordlock2` and `spend:ordlock2` patterns. When a listing is purchased or cancelled, the spend event routes the spending transaction to the overlay for cleanup.

OrdLock **v1** is deprecated. `pkg/parse/ordlock.go` still stores its decoded data (`dt:ordlock`) and indexes the seller under `own:{addr}` so the legacy sweep tool can cancel old listings, but it emits **no** public event, so v1 never reaches any topic or public listing index.

## Parser Event Reference

Each parser emits events that the event bridge routes:

| Parser | File | Events Emitted |
|--------|------|---------------|
| BSV21 | `pkg/parse/bsv21.go` | `bsv21:{tokenId}` |
| OrdLock | `pkg/parse/ordlock.go` | `ordlock2` (v2 only; v1 emits none) |
| BAP | `pkg/parse/bitcom.go` | `bap:{type}` |
| MAP | `pkg/parse/bitcom.go` | `map:type:{type}`, `map:subType:{subType}` |
| Collection | `pkg/parse/collection.go` | `map:collectionId:{id}` from `subTypeData` (after MAP; `_N` normalized) |
| OPNS | `pkg/parse/opns.go` | `opns:mine` |
| gib | `pkg/parse/gib.go` | `gib`, `gib:{origin}` (a reader's feed — no bridge routes these to a queue; `gib:{origin}` is the per-repository SSE subscription) |

Spend events are generated automatically by `SaveTransaction()` as `spend:{event}` for each event on a spent output.

## Queue Member Formats

| Source | Format | Size |
|--------|--------|------|
| JungleBus subscriber | Binary txid | 32 bytes |
| Event bridge | Binary outpoint (txid + LE vout) | 36 bytes |

`OverlaySync.parseQueueMember()` handles both:
- 36 bytes → `processOutpoint()` (GASP path if `ResolveDependencies`)
- 32 bytes → `processDirect()` (always direct submit)

## Known Issues

### Unmined transaction GASP resolution (OPL-1860)

For BSV21, `FindNeededInputs` in `go-overlay-services` requests ALL inputs for unmined transactions (no merkle proof), including non-topical funding inputs. The topic manager correctly ignores these in `IdentifyAdmissibleOutputs`, but GASP's dependency walker tries to resolve them first. A fix removing the post-dependency re-check is in bsv-blockchain/go-overlay-services#313.

### Queue-based ordering

When two transfers happen in quick succession (A→B, then B→C), both outpoints are queued via the event bridge. If the worker picks up C before A is admitted, GASP fails because A's outputs aren't in the overlay yet. No retry mechanism exists — the failed outpoint is removed from the queue. Evaluating direct `engine.Submit()` on broadcast instead of queuing (OPL-1860).

## Related Documentation

- `docs/architecture/OVERLAY_ARCHITECTURE.md` — Overlay engine, topics, lookups, per-topic storage
- `docs/architecture/BSV21_PIPELINE.md` — BSV21 dispatch, token manager, fee tracking
- `docs/architecture/INDEXING_ARCHITECTURE.md` — Parser events, save operations, engine hooks
