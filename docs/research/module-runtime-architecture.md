# Module Runtime Architecture

Status: **Draft — awaiting review**

## What This Document Covers

This document captures the architectural direction discussed on 2026-03-23. It supersedes the narrower "distributed compute vision" and "channel spec" documents by describing the full system — not just WASM modules and storage channels, but the entire runtime model, service graph, identity layer, and on-chain interface contracts.

## The Core Idea

1sat-stack stops being a Go application. It becomes a **module runtime** — a thin orchestrator that loads WASM modules, reads their channel declarations, resolves providers, and wires everything together. The same runtime runs on a server, a desktop, a phone, or in a browser. The modules are identical `.wasm` binaries everywhere. Only the provider configuration changes.

## Modules

A module is a WASM binary that:

- Exports typed functions (its API)
- Declares which channels it needs (its dependencies)
- Declares which channels it provides (its capabilities)
- Communicates through channels using protobuf-defined protocols
- Has no knowledge of how channels are fulfilled

Modules are stateless with respect to I/O. All state lives in channels. A module can be stopped, moved, or replaced without losing data.

### Examples of modules

| Module | Consumes | Provides |
|--------|----------|----------|
| parse | (none — pure function) | parse results |
| ordfs | beef, spends | content resolution |
| bap-overlay | topic-storage, lookup-db | bap topic manager, bap lookup |
| bsv21-overlay | topic-storage, lookup-db | bsv21 topic manager, bsv21 lookup |
| overlay-engine | topic-storage | admission, GASP sync |
| wallet | wallet-storage, chain-tracker, broadcast | BRC-100 wallet interface |
| monitor | wallet-storage, broadcast | proof tracking, rebroadcast |

## Channels

A channel is a bidirectional byte stream carrying a typed protocol. The protocol is defined by a protobuf service definition. The transport is the mux we already built — `[channel_id][length][payload]` over stdin/stdout for WASM, or `io.Pipe()` for in-process Go.

### Channel types are protocols, not implementations

Each channel type has a protobuf service definition that specifies exactly what requests and responses it supports. Examples:

**Beef channel:**
```
GetTx(txid) → raw_tx_bytes
PutTx(txid, raw_tx_bytes) → ok
GetMerklePath(txid) → merkle_path
HasTx(txid) → bool
```

**Spend channel:**
```
GetSpend(txid, vout) → spending_txid | null
SetSpend(txid, vout, spending_txid) → ok
```

**Wallet channel:**
```
CreateAction(description, outputs, labels) → action_result
SignAction(reference, spends, signatures) → signed_result
ListOutputs(basket, tags, include_envelope) → outputs
GetPublicKey(protocol_id, key_id, counterparty) → public_key
CreateSignature(protocol_id, key_id, counterparty, data) → signature
```

**Topic manager channel:**
```
IdentifyAdmissibleOutputs(parsed_beef, topic) → admittance_instructions
GetDocumentation() → docs
```

**Lookup service channel:**
```
Lookup(question) → answer
GetDocumentation() → docs
```

A module that declares `needs: beef` imports the beef proto and gets a typed client. It doesn't know if the provider is local Badger, a REST API, or a P2P peer.

### No RESP

RESP (Redis protocol) was an earlier design choice for storage channels. It's been replaced by protobuf service definitions for all channel types. Redis/Badger are implementation details of specific providers, not a channel protocol.

### Channel resolution

The runtime resolves channels through a provider chain. Configuration specifies, for each channel a module needs, an ordered list of providers to try:

```yaml
channels:
  beef:
    - provider: local-cache
      params: { max_size: 100MB }
    - provider: peer
      params: { peer_id: "Qm..." }
    - provider: junglebus
      params: { endpoint: "https://..." }
  spends:
    - provider: local-cache
    - provider: peer
      params: { peer_id: "Qm..." }
```

The runtime tries providers in order. Cache miss → next provider. The module never sees the chain — it just gets a response.

### SQLite

SQLite is not a channel. Lookup services that need relational storage get a SQLite file via WASI filesystem APIs. The host manages the file lifecycle and replication (Litestream, LiteFS, cr-sqlite, etc.). The module uses SQLite directly through its language's SQLite library.

## The Runtime

The runtime is the only non-WASM component. It's written in Go (using Wazero) for server/desktop, and in JavaScript/TypeScript for browser environments. It does:

1. **Load modules** — read `.wasm` binaries from disk, ORDFS, or wherever
2. **Read declarations** — each module declares its channel requirements and capabilities
3. **Resolve providers** — for each required channel, look up the provider chain from config
4. **Wire channels** — set up the mux, connect channel IDs to providers
5. **Start modules** — instantiate WASM, begin processing
6. **Expose interfaces** — map module capabilities to HTTP endpoints (BRC-103/104 auth) and libp2p protocol handlers (same auth over P2P)

The runtime is intentionally thin. It doesn't contain business logic. It's configuration and plumbing.

### HTTP and P2P are transport adapters

Every channel a module provides can be exposed simultaneously as:

- An HTTP endpoint (REST with BRC-103/104 mutual auth)
- A libp2p protocol handler (same auth over P2P streams)

The transport adapter translates between the external protocol (HTTP request, libp2p stream) and the internal channel protocol (protobuf over mux). The module doesn't know which transport the request came from.

### Identity

All peer communication is authenticated through the wallet's root identity key. This key is also the libp2p peer ID. When module A on node X talks to module B on node Y, the communication is:

1. libp2p stream opened between peer IDs
2. BRC-103 mutual authentication using wallet identity keys
3. Optional payment negotiation (BRC-105/109) if the provider requires it
4. Then the channel protocol flows — same protobuf bytes as a local channel

## Data Serialization

All structured data crossing module boundaries uses **Protocol Buffers**. This applies to:

- Function call arguments and return values (WASM exports/imports)
- Channel request/response payloads
- Inter-module communication

Protobuf provides:
- Static codegen for Go, Zig, TypeScript/AssemblyScript
- No runtime reflection or dynamic parsing inside WASM
- Compact binary format
- Schema evolution via field numbering

### Why not gRPC?

gRPC is protobuf + HTTP/2 transport. We use the same protobuf service definitions and serialization, but our transport is the channel mux (for WASM) and libp2p streams or HTTP (for network). gRPC's HTTP/2 framing doesn't map to stdin/stdout. But the `.proto` service definitions are identical — a gRPC client could talk to our HTTP endpoints if we wanted.

## On-Chain Interface Contracts

Proto files that define channel protocols can be recorded on-chain as inscriptions, addressed by outpoint. This makes them:

- **Immutable** — the definition at outpoint X never changes
- **Universally addressable** — any module anywhere can reference it
- **Verifiable** — the publisher signed the transaction with their identity key
- **Timestamped** — the block proves when it was published

A module's manifest references channel types by outpoint:

```
channels:
  - type: "abc123_0"   # points to beef.proto inscription
    role: consumer
  - type: "def456_0"   # points to ordfs.proto inscription
    role: provider
```

Two modules pointing to the same outpoint are guaranteed to speak the same protocol. No version confusion, no drift.

### Version evolution

Publishing a new version means inscribing an updated proto and creating a new outpoint. Old modules keep their references. New modules point to the new outpoint. Both coexist — no breaking changes, ever.

A module's outpoint can reference its dependency outpoints, forming an immutable dependency graph. Walking the graph from any entry point gives you the complete, verified set of interfaces and code that module depends on. This is content-addressed versioning with proof of existence — like git but with Bitcoin's guarantees.

### Service discovery

"What peers provide channel type `abc123_0`?" becomes an overlay lookup question. Peers announce their capabilities (which channel outpoints they provide) on the P2P network. Discovery is just another overlay.

## BEEF Handling

Raw BEEF bytes arrive from the network (JungleBus, P2P, direct submission). The host deserializes the BEEF once, computing all txids (expensive hashing). The parsed result — transactions accessible by txid with merkle paths — is serialized as a `ParsedBeef` protobuf message and passed to modules that need it.

Modules never re-parse or re-hash BEEF. They receive the pre-computed `ParsedBeef` and access transactions by txid.

The `ParsedBeef` protobuf carries:
- BUMPs (full merkle paths with block heights)
- Transactions map: txid → raw transaction bytes + bump index
- All ancestor transactions included (full SPV chain)

## Parse Module

The parse module is the first WASM module. It's a pure function — no channels needed. It receives `ParsedBeef`, iterates outputs and spent inputs, runs all protocol parsers, and returns `[IndexedOutput]`.

The parser library (inscription, BSV21, MAP, BAP, lock, cosign, OPNS, ordlock, shrug, etc.) is compiled into the parse module as static Zig code. The same parser source code is also compiled into topic manager modules that need it.

14 parsers are implemented in Zig. BSV21 (needs JSON parsing) and origin (needs ORDFS channel) are pending.

### Current Zig parser status

| Parser | Status | Notes |
|--------|--------|-------|
| 1sat | Done | Checks satoshis == 1 |
| p2pkh | Done | Prefix-based P2PKH owner detection |
| lock | Done | Full LockPrefix + LockSuffix from go-templates |
| inscription | Done | OP_FALSE OP_IF "ord" envelope |
| cosign | Done | 7-chunk sliding window |
| opns | Done | Full contract prefix via @embedFile |
| ordlock | Done | Full OrdLockPrefix + OrdLockSuffix |
| shrug | Done | Tag + outpoint + OP_2DROP pattern |
| bitcom (base) | Done | OP_RETURN pipe-splitting |
| B | Done | Data + media type + encoding |
| MAP | Done | CMD + key-value pairs |
| AIP | Done | Algorithm + address + signature |
| BAP | Done | Type-specific field parsing |
| SIGMA | Done | Algorithm + signer + signature |
| BSV21 | Not started | Needs JSON parsing of inscription content |
| Origin | Deferred | Needs ORDFS channel — stays in Go for now |

## What 1sat-stack Becomes

The Go codebase transitions from a monolithic application to:

1. **The runtime** — Wazero-based module loader, channel mux, provider resolution, HTTP/P2P adapters
2. **Provider implementations** — Go code that backs channels (Badger for beef storage, SQLite for lookups, JungleBus client, ARC for broadcast)
3. **WASM modules** — Zig binaries that contain the actual business logic (parsers, topic managers, lookups, eventually the overlay engine)

The existing Go packages (`pkg/beef`, `pkg/spends`, `pkg/txo`, etc.) become provider implementations behind channel interfaces. The overlay Go code (`pkg/overlay`, `pkg/bap`, `pkg/bsv21`, etc.) gets ported to Zig WASM modules over time.

Native Go modules keep working alongside WASM modules during the transition. A Go shim implements the channel protocol in-process, so existing Go code can consume and provide channels without WASM.

## Relationship to Prior Documents

- **distributed-compute-vision.md** — still valid conceptually, but this document is more concrete about the runtime model, channel protocols, and on-chain contracts
- **channel-spec.md** — the mux framing protocol is still correct; the RESP payload decision is superseded by protobuf service definitions; the SQLite-as-file decision stands
- **engine-migration.md** — Phase 0 (redcon + Badger) and the Zig parser work remain valid foundations; the `store.Store` → `*redis.Client` migration is obsoleted by the channel model; engine storage redesign (Phase 2) will use protobuf channels instead of RESP

## Open Questions

1. **Proto service definitions** — need to write the actual `.proto` files for each channel type. Start with beef and spends as they're the simplest.
2. **Module manifest format** — how does a module declare its channel requirements and capabilities? Embedded in the WASM binary? Separate file? On-chain inscription?
3. **Provider chain semantics** — how does fallback work? First success? Merge results? Configurable per channel type?
4. **Browser runtime** — the Go/Wazero runtime doesn't run in browsers. What's the browser equivalent? Wasmtime-in-browser? Custom JS runtime?
5. **gRPC compatibility** — should our HTTP endpoints speak gRPC-Web so existing gRPC clients can connect? Or is our own HTTP mapping sufficient?
6. **Payment integration** — how does BRC-105/109 payment flow integrate with channel resolution? Is it part of the provider chain or a separate concern?
7. **Libp2p integration** — how do we map channel protocols to libp2p protocol IDs? One protocol per channel type? Multiplexed?
8. **On-chain proto publishing** — tooling for inscribing proto files and creating module manifests that reference them.
9. **Migration path** — detailed sequencing from current Go monolith to runtime + modules. Which modules first? How do we keep production running during the transition?
