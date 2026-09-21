# Project Plans

| Plan | Status | Description |
|------|--------|-------------|
| [BRC-169 lifecycle gate](./BRC169_LIFECYCLE_GATE.md) | **In Progress** | OPL-4473 — Real HTTP submit/import/lookup, disk persistence, and upstream empty-query gate |
| [Market API & OPNS Validation](./market-api-opns-validation.md) | **In Progress** | Rename ordlock→market, origin BLOB fix, OPNS bulk validate, SDK clients |
| [GASP Wire Protocol](./gasp-wire-protocol.md) | **In Progress** | OPL-1135 — Binary wire protocol for GASP over libp2p streams, with payment envelope |
| [Admin UI Plan](./admin-ui-plan.md) | **In Progress** | OPL-1186 — First-run wizard, settings page, clean-slate UI redesign |
| [Sweep UI](./sweep-ui.md) | **In Progress** | OPL-700 — Standalone sweep UI at `/1sat/sweep/` for legacy wallet migration |
| [Landing Page](../superpowers/plans/2026-03-17-landing-page.md) | **In Progress** | OPL-1404 — Terminal-aesthetic landing page at `/1sat/` |
| BSV21 TopicWorker → OverlaySync | **In Progress** | OPL-1468 — Unify BSV21 token workers with OverlaySync, add OnProcessed, delete TopicWorker |
| [Docker Compose Service Split](./docker-compose-service-split.md) | **Not Started** | Split overlays into independent services with dynamic compose generation |
| [Persistent Logging](./2026-03-26-persistent-logging.md) | **Not Started** | SQLite log persistence, multi-handler, module tagging, admin log viewer |
| [External Arcade Migration](./external-arcade-migration.md) | **In Progress** | Migrate to arcade.gorillapool.io via new `/1sat/tx` route, always-on SSE consumer with event broker, custom overlay broadcaster |
| [Shrug Token Parity](./shrug-parity.md) | **In Progress** | Shrug (¯\\_(ツ)\_/¯) token to BSV-21 parity — templates and spec done, stack topic managers + lookup next |

## Completed Plans

| Plan | Status | Description |
|------|--------|-------------|
| Wallet Connect Flow | **Complete** | yours-wallet auth and popup fixes done |
| [Admin & OpNS Registration](../archive/2026-03-03-admin-opns-registration.md) | **Complete** | Admin setup, OPNS crawl, lookups wired up |
| [Overlay Storage Isolation](../archive/overlay-storage-isolation.md) | **Complete** | Per-topic SQLite, TxTopicIndex, dead code removed |
| OrdLock Overlay | **Complete** | Deployed to rack, syncing from block 783968 |
| [TXO Lookup Fixes](../archive/TXO_LOOKUP_FIXES.md) | **Complete** | LoadOutputsByTxid fixed to use event index |
| [Config Infrastructure Design](../archive/config-infrastructure-design.md) | **Complete** | OPL-1185 — Two-layer config (static + config store), SQLite backend |
| [Admin-Configurable Settings](../archive/admin-configurable-settings.md) | **Complete** | OPL-1183 — Store-first config, setup wizard, module hierarchy |
| [Config Store Schema](../archive/config-store-schema.md) | **Complete** | SQLite config store schema and key format |
| [Event-Driven Overlay Routing](../archive/event-driven-overlay-routing.md) | **Complete** | PubSub patterns, EventBridge, parser events, all overlay workers running |
| [Persistent Paymail & Sessions](../archive/PERSISTENT_PAYMAIL_AND_SESSIONS.md) | **Complete** | Paymail SQLite store, Badger session manager |
| [BAP Query-Time Resolution](../archive/bap-query-time-resolution.md) | **Complete** | Authority validation moved from write-time to read-time |
| [Origin Indexer](../archive/origin-indexer.md) | **Complete** | Origin parser emitting events for transferred ordinals |
| [Sync Client Packages](../archive/sync-client-packages.md) | **Complete** | OPL-1323 — pkg/sync/ for BRC-100 wallet transaction ingestion |
| [OrdLock Direct Worker](../archive/ordlock-direct-worker.md) | **Complete** | Direct worker replacing overlay engine for OrdLock |
| [BAP API Consolidation](../archive/bap-api-consolidation.md) | **Complete** | BAP API cleanup, backup format mapping, sign-in flow |
| [Consolidation Plan](../archive/PLAN.md) | **Complete** | Original project consolidation from overlay/indexer/BSV21/ORDFS |

## Status Legend

- **Not Started**: Plan created, work not begun
- **In Progress**: Active development
- **BLOCKED**: Waiting on dependency or issue resolution
- **Complete**: Work finished and verified
