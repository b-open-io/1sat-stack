# WASM Architecture Audit

Status: **Complete — see runtime-strategy.md for decisions**

Full audit of 1sat-stack (Go) and 1sat-sdk (TypeScript) to map what moves to WASM, what stays on the host, and where the two implementations diverge.

## Current State

### What's built
- 14 Zig parsers compiled to parse_tx.wasm (84KB)
- WASM exports: `parse_beef` (raw BEEF) and `parse_atomic_beef` (proto-encoded AtomicBeef)
- Go host: Wazero wrapper (`pkg/wasmparse/`), Transaction→AtomicBeef converter, wired into `IngestCtx.ParseTx`
- TS host: `@1sat/engine` package with Engine class, protobuf encode/decode
- Proto definitions: `beef.proto`, `parse.proto`, per-parser protos

### What's not settled
- How WASM modules access host storage (channels? imported functions? other?)
- How WASM modules make async requests (lookup data, validate against overlay)
- Whether overlay engine itself moves to WASM
- Browser WASM runtime details

## Parse Flow Comparison

### Go (1sat-stack)

```
BEEF bytes → IngestCtx.IngestTx()
  → ParseTx() → [WASM or Go-native parsers]
    → Per output: events[] + owners[] + data map
    → Per spend: same
  → IndexContext with Outputs[] and Spends[]
  → Save to OutputStore (events as sorted sets, output metadata as hashes)
```

No basket. No protocol. No owner matching against a wallet. This is server-side indexing for the overlay — it stores events and owners for global lookup, not for any specific wallet.

### TypeScript (1sat-sdk)

```
BEEF bytes → parseTransaction() [in internalizeBeef.ts]
  → For each output/spend:
    → Run each indexer.parse(txo) sequentially
    → Each returns: { data, tags, owner?, basket?, protocol?, content? }
    → First indexer to set owner/basket/protocol wins
  → Summarize phase:
    → Each indexer.summarize(ctx) with full ParseContext
    → Cross-output validation (BSV21 token flows, origin tracing)
    → Some make HTTP calls (BSV21 overlay check, OrdFS metadata)
  → buildInternalizeOutput():
    → Match txo.owner against wallet's address derivations
    → Determine final protocol (wallet payment vs basket insertion)
    → Non-fund baskets forced to basket insertion
  → wallet.internalizeAction()
```

Basket assignment happens in indexers during parse. Protocol override happens in buildInternalizeOutput. Owner matching against wallet addresses happens at internalize time.

### Go Internalization (1sat-stack)

```
BEEF bytes → Internalizer.FromSync()
  → Parse BEEF to get transaction
  → For each output:
    → Extract P2PKH address from locking script
    → Match against derivations map
    → If match: add to InternalizeOutput with WalletPayment protocol
  → wallet.InternalizeAction()
```

Only handles P2PKH. No basket assignment. No protocol differentiation. No ordinals, tokens, locks, or custom scripts.

## Divergences

| Concern | Go (1sat-stack) | TypeScript (1sat-sdk) |
|---------|-----------------|----------------------|
| Basket assignment | Not implemented | In indexer.parse() — per protocol |
| Protocol assignment | Hardcoded WalletPayment | In indexer.parse() + override in buildInternalizeOutput |
| Owner matching | Separate pass (Internalizer.FromSync) — P2PKH only | Inside indexer.parse() via owners Set |
| Summarize phase | Not implemented | Per-indexer with full ParseContext |
| BSV21 validation | Server-side in overlay | Client-side HTTP to overlay in summarize |
| Origin resolution | Server-side in ordfs package | Client-side HTTP to OrdFS in OriginIndexer.summarize |
| Custom script handling | Not handled in sync | basket insertion protocol, manual unlock |

## What Belongs Where

### WASM (pure business logic, no I/O)

| Component | Status | Notes |
|-----------|--------|-------|
| Script parsers | Done (14 Zig parsers) | BSV21 pending (needs JSON) |
| Basket assignment | Not started | Deterministic from parse output — content type routes to basket |
| Protocol assignment | Not started | Deterministic from script type — custom scripts → basket insertion |
| Token flow validation | Not started | Sum inputs vs outputs, mark valid/invalid |
| Origin tracing logic | Not started | Satoshi position tracking through spend chains |
| Fee calculation | Not started | Input selection, change calculation |
| Topic manager admission | Not started | Per-protocol admission rules |

### Host (I/O, coordination, platform-specific)

| Component | Notes |
|-----------|-------|
| Storage backends | Redis, Badger, SQLite, IndexedDB — provide data to WASM via interface TBD |
| Network clients | JungleBus, OrdFS, overlay HTTP, ARC broadcast |
| Wallet SDK calls | createAction, signAction, internalizeAction |
| Worker lifecycle | Topic workers, sync workers, queue processing |
| Overlay engine coordination | Topic registration, GASP sync, remote peers |
| Address derivation | BRC-29 key derivation (crypto) |
| PubSub | Event broadcasting (Redis, SSE, channels) |

### Needs both (logic in WASM, data from host)

| Component | WASM logic | Host I/O needed |
|-----------|-----------|----------------|
| BSV21 summarize | Token flow validation rules | Token details from overlay (HTTP) |
| Origin resolution | Spend chain traversal logic | Load transactions from beef storage |
| Lookup services | Query logic, filtering, aggregation | SQLite database access |
| Overlay engine | Admission decisions, GASP protocol | Storage, network, peer management |

## The Hard Problem: WASM ↔ Host Data Access

The pure-function modules (parsers, basket routing) are solved — data in, result out, no callbacks.

The stateful modules (lookup services, overlay engine, origin resolution) need to request data during execution:
- "Give me the transaction for this txid"
- "What's the spend for this outpoint?"
- "Query this SQLite table"
- "What token details exist for this token ID?"

### Options discussed (none settled)

1. **Channel mux** — bidirectional byte stream over stdin/stdout with protobuf service protocols. Module sends request on channel, host fulfills, module continues. Documented in module-runtime-architecture.md but not implemented.

2. **Host-imported functions** — WASM imports that the host implements. Module calls `host_get_tx(txid_ptr, txid_len) → ptr`. Synchronous from WASM's perspective. Simple but requires defining every host function upfront.

3. **WASI filesystem for SQLite** — lookup services open SQLite files via WASI. Host manages the files. Works for read-heavy query workloads. Doesn't help with network-dependent data.

4. **Two-phase execution** — WASM returns "I need these txids" as part of its result. Host fetches them, calls WASM again with the data. No callbacks, but potentially many round trips.

5. **Pre-populated context** — Host gathers all data the module might need upfront, passes it all in. Simple but wasteful — hard to predict what data a module needs without running it.

### What each module type actually needs

**Topic managers**: Receive a parsed beef + topic name. Return admission instructions. May need to check existing state (e.g., "is this token already deployed?"). Mostly pure logic with occasional storage lookups.

**Lookup services**: Receive queries. Return results from indexed data. Heavy database access (SQLite). Read-heavy, write on admission.

**Overlay engine**: Orchestrates topic managers and lookup services. Manages GASP sync. Heavy coordination — may not belong in WASM at all, or may be a thin WASM module that delegates all I/O to host channels.

**Address sync / internalization**: Receives parsed beef + wallet addresses. Returns list of outputs to internalize with basket/protocol. Needs: parsed output data (from WASM parser), address matching (pure logic), basket routing (pure logic). Actually mostly pure — the only I/O is loading the beef and calling wallet.internalizeAction, both of which are host-side bookends.

## Recommended Next Steps

1. **Settle on host↔WASM data access pattern** before building more WASM modules. Build a proof of concept with the simplest stateful module (topic manager with one storage lookup) to validate the chosen approach.

2. **Unify basket/protocol assignment** as a standalone function (WASM or shared logic) that both Go and TS consume. Currently it's scattered across TS indexers and missing from Go entirely.

3. **Implement Go internalization for non-P2PKH outputs** — the Go Internalizer only handles P2PKH wallet payments. It needs ordinals, tokens, locks, etc.

4. **Define the overlay engine's WASM boundary** — is it one big module or many small ones? What's the minimum viable interface between the engine and host storage?

## References

- docs/research/module-runtime-architecture.md — full vision doc
- docs/research/channel-spec.md — channel mux framing (partially superseded)
- docs/plans/engine-migration.md — migration tracking
