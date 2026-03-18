# Admin-Configurable Server Settings

Status: **In Progress** (design discussion phase)

Linear: OPL-1183

## Problem

1sat-stack settings currently require config files (config.yaml) or environment variables. Operators must SSH in and edit files to change anything. We want settings configurable through the admin UI at runtime.

## Scope

This plan covers runtime configuration, but design discussion will also capture structural changes needed (routing, module reorganization, dependency cleanup) as they surface. Configuration is the focus, but not the boundary.

## Design Direction

We're moving toward a **store-first configuration model**, not layering a store on top of config files.

### The Vision

1. **Fresh install**: Server starts, auto-generates a private key (persisted to filesystem), initializes all always-on services with sensible defaults. Serves the setup UI at `/admin/setup/`.
2. **Setup wizard**: First question is deployment mode (local vs authenticated). Then walks through overlay selection and optional JungleBus configuration.
3. **Config lives in a dedicated config store**: Separate from the data store. Config data has different characteristics (small, rarely written, frequently read) and should not be intermixed with TXO/queue/overlay data.
4. **Config file / env vars as overrides**: For headless/automated deployments, or for sensitive values like private keys that shouldn't go through a browser UI.

### Deployment Modes

The first wizard question determines the auth model for all admin operations:

- **Local mode**: Unauthenticated admin. The server IS the user's wallet, running on their machine. Adding auth doesn't raise the security bar since the databases and keys are already accessible on the local filesystem.
- **Authenticated mode**: Admin operations require authentication via an external BRC-100 wallet (Yours Wallet, MetaNet Desktop, 1Sat Wallet, etc.). For remote/server deployments where the operator is not the machine owner.

The auth mode is self-protecting: changing it requires current-level auth. An authenticated server can't be flipped to unauthenticated without proving admin access first.

### Server Identity Key Bootstrap

1. **Default**: Server auto-generates a private key at first run, persists to filesystem. No user action needed.
2. **Override**: Environment variable or config file to bring your own key — for multi-frontend deployments sharing an identity, or importing an existing wallet.
3. **No wizard UI for keys**: Key material never passes through the browser. This is strictly an env var / config file concern.

The server key is used for: Wallet operations, P2P peer identity (libp2p secp256k1), BRC-42 payment derivation, and auth middleware.

### Always-On Services

These initialize automatically with working defaults and require no wizard configuration:

| Service | Default Config | Reconfigurable |
|---------|---------------|----------------|
| Store | Badger (embedded) | Switch to Redis, remote |
| PubSub | Embedded | Switch to Redis, remote |
| Chaintracks | Embedded (SQLite) | Switch to remote |
| Arcade | Embedded | Switch to remote |
| Wallet | Embedded (SQLite) | Switch to PostgreSQL or other SQL |
| P2P | Embedded | — |
| Admin | Embedded | — |
| Beef | Embedded | Switch to remote |
| TXO | Embedded | Switch to remote |
| ORDFS | Embedded | — (passive, zero cost if unused) |
| Indexer | Embedded | Parsers are hardcoded, not admin-configurable |
| MessageBox | Embedded | Wallet-to-wallet communication |

Reconfiguration happens through the admin UI after initial setup, not during the wizard.

### Setup Wizard Flow

1. **Auth mode**: Local (unauthenticated) or Authenticated (external wallet required)
2. **Overlay selection**: Which overlays to enable — BAP, OPNS, BSV21, BSocial, OrdLock (each is on/off except BSV21 which has topic scope)
3. **BSV21 topic scope** (if BSV21 enabled): Whitelist specific tokens or enable full discovery
4. **JungleBus** (optional): Per-overlay subscription IDs for service-provider deployments

Dependency chains are resolved automatically — enabling any overlay auto-enables the Overlay engine.

### Auth Mode Bootstrap (Open Question)

In authenticated mode, the very first admin connection happens before any admin key is registered. Likely "first person to hit the setup page wins" — common pattern in self-hosted tools. Details need more thought.

### Config Precedence

Two layers, not three:

1. **Static config** (env vars OR config file — same layer, operator's choice of format). For infrastructure: listen port, data dir, private key, storage backends, network. Read once at startup, immutable at runtime.
2. **Config store** (SQLite, separate from data store). For application settings: auth mode, overlay toggles, BSV21 topics, JungleBus subscriptions. Managed via admin UI, modifiable at runtime.

See `docs/plans/config-infrastructure-design.md` for the full design.

### Config Store Isolation

- Config store is separate from the data store (different instance/interface)
- Each 1sat-stack instance has its own config store — no sharing across instances
- Scaling is done by standing up additional instances pointing at shared infrastructure via remote mode, not by clustering or replicating config

## Module Hierarchy

See `docs/research/MODULE_DEPENDENCY_MAP.md` for the full analysis. Summary:

### Tiers

- **Tier 0 (Always-On Core)**: Store, PubSub, Chaintracks, Arcade, Wallet, P2P, Admin, Beef, TXO, ORDFS, Indexer, MessageBox
- **Tier 1 (Overlay Engine)**: Overlay
- **Tier 2 (Protocol Overlays)**: BSV21, BAP, BSocial, OPNS, OrdLock — all require Overlay
- **Tier 3 (Application Services)**: Owner (JungleBus-only)
- **Tier 4 (External-Facing)**: Auth, Paymail
- **Optional Data Source**: JungleBus (per-overlay subscription, service-provider concern)

### Implied Bundles

Enabling a leaf module auto-enables its dependency chain (always-on services are already running):

- **"I want BSV21 tokens"** → Overlay + BSV21
- **"I want paymail"** → Overlay + OPNS + Paymail (ORDFS and MessageBox already on)
- **"I just want ORDFS"** → Already running (always-on)
- **"I want full discovery"** → All overlays + JungleBus subscriptions

### Notable Quirks

- BSocial is the only overlay requiring MongoDB (external infrastructure dependency — needs special handling in wizard)
- P2P overlay bus is created inline during Overlay init, not a standalone module
- The wallet key doubles as libp2p peer identity (secp256k1), enabling BRC-42 payment derivation from peer IDs
- Owner sync is JungleBus-only — it requires the external full-chain index
- ORDFS is always-on but currently hard-depends on JungleBus — needs rewiring to resolve from local storage
- Paymail has hard dependencies on both OPNS (name resolution) and ORDFS (profile content)

## Storage Architecture

### Store Separation

Three distinct storage concerns:

- **Config store** — Application settings, auth mode, module toggles. Separate instance/interface from data. Not shared across instances.
- **Data store** — TXO data, processing queues, events. High-volume operational data using sorted sets (same pattern whether Badger or Redis).
- **Per-overlay stores** — Each overlay owns its own database. Protocol-specific data and queries are scoped here, not in the global data store.

### TXO Event Scoping (Structural Change)

The TXO module currently indexes a broad set of events globally. This needs to be reduced:

- **Overlays own their query surface.** Protocol-specific searches belong in overlay-scoped storage.
- **Owner events** are likely the only events needing global indexing, and may be further scoped to the owner sync module.
- **`ev:txid:*` events** may only be needed for internal operations (rollbacks), not as an external query surface.
- **BSV21 is currently querying global TXO events** instead of its own overlay storage — this is a bug to fix independently.

### Indexer

The indexer is always-on. Its parsers are hardcoded and map directly to supported modules — they ship with the binary. Parser selection is not admin-configurable; adding or removing parsers happens when modules are added or removed from the codebase.

## Admin API

Audit complete (OPL-1239). Existing admin API is functional and not broken by overlay storage isolation. The redesign is an extension, not a tear-down.

### Existing (Keep)
- User management (CRUD users, approve/deny requests)
- BSV21 management (whitelist, blacklist, workers)
- Overlay management (active topics/lookups, remote config)
- Progress tracking (sync progress read/update/delete)
- OpNS crawl trigger
- Generic data routes (behind dev-mode flag, controllable by admin)

### New
- **Setup flow** at `/admin/setup/*` — supersedes old setup. Auth mode, overlay toggles, BSV21 topics, JungleBus subs, backed by ConfigStore.
- **Settings management** at `/admin/api/config/*` — GET/PUT config store values, read-only for static layer.
- **Local mode** — unauthenticated admin when running locally.
- **isAdmin flag** on users — basic admin permission, no broader permissions model yet.

## Resolved Design Questions

1. ~~Wizard completion~~ → Restart for v1
2. ~~Auth mode change~~ → Restart required
3. ~~Tuning params~~ → Config store (same work, more flexible)
4. ~~Migration~~ → None, dev environments re-run setup
5. ~~Config precedence~~ → Two layers: static (env/file) + config store (SQLite)
6. ~~Config file role~~ → Keep for static layer convenience, no longer holds runtime settings
7. ~~Admin API~~ → Extend existing, not tear-down. Setup at `/admin/setup/*`, config at `/admin/api/config/*`
8. ~~Data write routes~~ → Keep behind dev-mode flag
9. ~~User permissions~~ → isAdmin flag, nothing broader yet

## Implementation Plan

See Linear epic OPL-1183 for full issue tree. Summary:

| Issue | Title | Status |
|-------|-------|--------|
| OPL-1185 | Design: Configuration infrastructure | Done |
| OPL-1239 | Audit and redesign admin API | Backlog |
| OPL-1186 | Design: Admin UI | Backlog |
| OPL-1236 | Implement ConfigStore (SQLite) | Backlog |
| OPL-1237 | Always-on module initialization | Backlog |
| OPL-1238 | Integrate ConfigStore into startup flow | Backlog |
| OPL-1240 | Implement admin API (wizard + settings + data) | Backlog |
| OPL-1187 | Fix: ORDFS spend lookups without JungleBus | Backlog |
| OPL-1188 | Fix: BSV21 migrate off global TXO events | Backlog |
| OPL-1189 | Evaluate: OrdLock as overlay | Backlog |
| OPL-1190 | Evaluate: Global event persistence necessity | Backlog |
