# Module Dependency Map

Analysis of `cmd/server/config.go` initialization sequence and inter-module dependencies.

## Core Purpose

1sat-stack processes and queries blockchain data through multiple lenses (overlays). The overlay services wire up those lenses. The same stack runs as a single-user local application or as a multi-user server — the deployment context determines which features are enabled.

### Data Ingress Paths

Transactions enter the system through three independent paths:

1. **Arcade** — User broadcasts a new transaction
2. **Overlay sync (GASP)** — Peer overlays push/pull records between instances
3. **JungleBus** — External full-chain streaming (optional, service-provider concern)

## Dependency Graph

```
 Always-On Core (Tier 0)
 ┌──────────────────────────────────────────────────────────┐
 │  Store  PubSub  Chaintracks  Arcade  Wallet  P2P  Admin │
 │  Beef   TXO     ORDFS                                   │
 └──────────────────────────┬───────────────────────────────┘
                            │
              │
              ▼
        ┌───────────┐
        │  Overlay  │
        └─────┬─────┘
              │
    ┌─────┬───┼───┬────────┐
    ▼     ▼   ▼   ▼        ▼
  BAP  BSV21 OPNS OrdLock BSocial
              │
              ▼
        ┌──────────┐
        │  Paymail │◄── ORDFS, MessageBox
        └──────────┘

 Separate:
   Owner ──▶ requires JungleBus (full chain index)
   Auth ──▶ requires Wallet

 Optional data source:
   JungleBus ──▶ per-overlay subscription feeds
```

## Module Tiers

### Tier 0: Always-On Core

These services initialize automatically with sensible defaults. No user configuration required for basic operation, but settings are reconfigurable through the admin UI after setup.

| Module | Config Key | Modes | Default | Provides |
|--------|-----------|-------|---------|----------|
| Store | `store` | embedded, remote | embedded | Key-value persistence for all services |
| PubSub | `pubsub` | embedded, remote | embedded | Event bus, SSE streams, queue consumers |
| Chaintracks | `chaintracks` | embedded, remote | embedded | Header verification, block height, SPV |
| Arcade | `arcade` | embedded, remote | embedded | Transaction broadcast, mempool events |
| Wallet | `wallet` | embedded | embedded | Identity, tx signing, BRC-100 (SQLite default, reconfigurable to Postgres) |
| P2P | `p2p` | embedded | embedded | Peer connectivity |
| Admin | `admin` | embedded | embedded | Admin UI and API |
| Beef | `beef` | embedded, remote | embedded | Transaction storage with SPV proofs |
| TXO | `txo` | embedded, remote | embedded | Output indexing, parsing, overlay routing |
| ORDFS | `ordfs` | embedded | embedded | Content serving for inscriptions (passive, zero cost if unused) |
| Indexer | `indexer` | embedded | embedded | Transaction parsing, arcade event handling. Parsers are compile-time and map to supported modules. |
| MessageBox | `messagebox` | embedded | embedded | Wallet-to-wallet communication |

### Tier 1: Overlay Engine

| Module | Config Key | Modes | Default | Hard Deps | Provides |
|--------|-----------|-------|---------|-----------|----------|
| Overlay | `overlay` | embedded, disabled | disabled | TXO | Engine, topic/lookup registration, P2P bus |

### Tier 2: Protocol Overlays (all require Overlay)

| Module | Config Key | Modes | Default | Hard Deps | Extra Deps | Notes |
|--------|-----------|-------|---------|-----------|------------|-------|
| BSV21 | `bsv21` | embedded, remote, disabled | disabled | TXO | Overlay (topic registration) | Per-token topic config: whitelist specific tokens or full discovery |
| BAP | `bap` | embedded, disabled | disabled | Overlay (TopicDB) | Beef (for sync) | Simple on/off |
| BSocial | `bsocial` | embedded, disabled | disabled | MongoDB | Overlay (topic registration) | Simple on/off; requires external MongoDB |
| OPNS | `opns` | embedded, disabled | disabled | Overlay (TopicDB) | Beef (for crawl/sync) | Simple on/off |
| OrdLock | `ordlock` | embedded, disabled | disabled | Overlay | Beef | Simple on/off |

### Tier 3: Application Services

| Module | Config Key | Modes | Default | Hard Deps | Provides |
|--------|-----------|-------|---------|-----------|----------|
| Owner | `owner` | embedded, disabled | disabled | TXO, Beef, Indexer, JungleBus | Address/UTXO sync |

### Tier 4: External-Facing Services

| Module | Config Key | Modes | Default | Hard Deps | Soft Deps |
|--------|-----------|-------|---------|-----------|-----------|
| Auth | `auth` | conditional | — | Wallet | — |
| Paymail | `paymail` | enabled, disabled | disabled | OPNS, ORDFS, MessageBox | — |

### Optional Data Source

| Module | Config Key | Modes | Default | Hard Deps | Provides |
|--------|-----------|-------|---------|-----------|----------|
| JungleBus | `junglebus` | enabled, disabled | disabled | — | Transaction streaming from full chain index |

JungleBus is fully optional. No module hard-depends on it. If any code path currently requires JungleBus, that is a bug to fix. Each overlay can optionally take a JungleBus subscription ID to feed its processing queue.

## Hard Dependency Gates

These are `if X == nil { skip }` or `return error` checks in config.go:

| Line | Gate | Effect |
|------|------|--------|
| 445 | Overlay requires TXO | Overlay skipped if TXO disabled |
| 479 | BSV21 requires TXO | BSV21 skipped if TXO disabled |
| 527 | BAP requires Overlay.TopicDB | BAP skipped if Overlay disabled |
| 563 | BSocial requires MongoDB | BSocial skipped if no MongoDB |
| 600 | OPNS requires Overlay.TopicDB | OPNS skipped if Overlay disabled |
| 642 | OrdLock requires Beef | OrdLock skipped if Beef disabled |
| 679 | Indexer requires TXO + Beef | Indexer fails without both |
| 747 | Owner requires TXO + Beef + Indexer | Owner skipped without all three |
| 765 | Admin requires Store | Admin skipped if Store disabled |
| 803 | Auth requires Wallet | Auth skipped if Wallet disabled |

## Implied Module Bundles

Enabling certain features implies a chain of required modules:

**"I want to index BSV21 tokens"**
→ Overlay + BSV21 (Store, PubSub, Beef, TXO already always-on)

**"I want paymail"**
→ Overlay + OPNS + Paymail (ORDFS and MessageBox already always-on)

**"I just want ORDFS content serving"**
→ Already running (always-on)

**"I want full discovery / service provider"**
→ All overlays + JungleBus subscriptions per overlay

## JungleBus Subscribers

Optional per-overlay configuration. Each overlay can take a subscription ID to feed its processing queue from the JungleBus full-chain stream.

| Subscriber | Config Source | Queue Key | Requires |
|-----------|-------------|-----------|----------|
| BSV21 | `bsv21.sync.subscription_id` | `q:bsv21` | Store, Chaintracks, JungleBus |
| BAP | `bap.sync.subscription_id` | `q:bap` | Store, Chaintracks, JungleBus |
| BSocial | `bsocial.sync.subscription_id` | `q:bsocial` | Store, Chaintracks, JungleBus |
| Ingest | `indexer.sync.subscription_ids` (multiple) | `q:ingest` | Store, Chaintracks, JungleBus |

## P2P Overlay Bus

Created inline during Overlay init (not a separate module). Depends on:
- `overlay.p2p.enabled` = true
- Store (for P2PBus internals)
- Server private key (from Wallet, always available since Wallet is always-on)

The wallet key doubles as the libp2p peer identity (secp256k1), enabling BRC-42 payment derivation from peer IDs.

## BSV21 Topic Configuration

BSV21 is unique among overlays in having per-topic granularity:

- **Whitelist mode**: Operator specifies specific token topics to track (local/personal use)
- **Discovery mode**: Track all BSV21 tokens as they appear (service provider use)

All other overlays (BAP, OPNS, BSocial, OrdLock) are simple on/off toggles.

## Owner Sync

Owner sync is the one module that genuinely requires JungleBus. It needs the external full-chain index to discover UTXOs for arbitrary addresses — there is no overlay equivalent for this. Without JungleBus, Owner sync cannot function.

## Storage Architecture

### Store Separation

- **Config store** — Dedicated store for application settings, auth mode, module toggles. Separate from data. Small, rarely written, frequently read. Not shared across instances.
- **Data store** — TXO data, events, processing queues. High-volume operational data. Same access patterns (sorted sets) whether backed by Redis or Badger.
- **Per-overlay stores** — Each overlay (BAP, OPNS, BSV21, BSocial, OrdLock) owns its own database for domain-specific data and queries.

### TXO Global Event Scoping

The TXO module currently indexes a broad set of events globally (`ev:*` keys in the data store). This should be drastically reduced:

- **Overlays own their query surface.** Protocol-specific data (token holders, identity lookups, domain names) should be queried from overlay-scoped storage, not the global event index.
- **Owner events** may be the only events that need global indexing, and even those could potentially be scoped to the owner sync module.
- **`ev:txid:*` events** — used for internal output-to-transaction mapping. May only be needed for internal operations like rollbacks, not as an external query surface.
- **The general principle**: the global store handles transaction storage and overlay routing. Overlays handle their own indexing and querying.

## Known Issues to Address

### Structural Issues (independent of configuration work)

1. **ORDFS spend lookups**: ORDFS needs to resolve spends the same way Beef does — check local TXO store first, fall back to JungleBus only if configured. Currently hard-depends on JungleBus.
2. **OrdLock as overlay**: OrdLock needs to be wired up as a proper overlay. Needs more design work — functional as standalone for now.
3. **BSV21 querying global TXO events**: BSV21 routes still query the global event index (`ev:id:*`, `ev:sym:*`, `ev:p2pkh:*`) instead of using its own overlay-scoped storage.

### Evaluation Items (post-configuration work)

4. **Do events need global persistence?** Events are parsed and used for routing into overlay queues and pub/sub. The question is whether they need to be persisted to the store at all, or if routing via pub/sub and queue placement is sufficient. This is a space-saving and clarity optimization, not a blocker for other work.

### Previously Noted

- **Beef JungleBus fallback**: The lookup chain should simply skip JungleBus if not configured. All lookups fall back to local storage.
- **Over-indexed global events**: The TXO event index stores far more event types than are consumed. Most protocol-specific events should move to overlay-scoped storage.

## Notes for Setup Wizard Design

- Always-on services (Chaintracks, Arcade, Wallet) have sensible defaults and don't need wizard configuration. They're reconfigurable later through admin UI.
- Enabling any overlay auto-enables the Overlay engine (all other dependencies are already always-on).
- BSocial is the only overlay requiring MongoDB (external infrastructure dependency — needs special handling in wizard).
- The "remote" mode for modules means connecting to another 1sat-stack instance rather than running locally.
- Each instance has its own config store. Scaling is done by standing up additional instances pointing at shared infrastructure via remote mode, not by clustering or replicating config.
