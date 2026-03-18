# Project Plans

| Plan | Status | Description |
|------|--------|-------------|
| [Market API & OPNS Validation](./market-api-opns-validation.md) | **In Progress** | Rename ordlock→market, origin BLOB fix, OPNS bulk validate, SDK clients |
| [GASP Wire Protocol](./gasp-wire-protocol.md) | **In Progress** | Binary wire protocol for GASP over libp2p streams, with payment envelope |
| [Admin-Configurable Settings](./admin-configurable-settings.md) | **In Progress** | OPL-1183 — Store-first config, setup wizard, module hierarchy (design phase) |
| [Admin UI Plan](./admin-ui-plan.md) | **In Progress** | OPL-1186 — First-run wizard, settings page, clean-slate UI redesign |
| [Config Infrastructure Design](./config-infrastructure-design.md) | **Complete** | OPL-1185 — Two-layer config (static + config store), SQLite backend, no migration needed |
| [Event-Driven Overlay Routing](./event-driven-overlay-routing.md) | **Complete** | Deployed to rack — PubSub patterns, EventBridge, parser events, all overlay workers running |

## Completed Plans

| Plan | Status | Description |
|------|--------|-------------|
| Wallet Connect Flow | **Complete** | yours-wallet auth and popup fixes done |
| [Admin & OpNS Registration](./2026-03-03-admin-opns-registration.md) | **Complete** | Admin setup, OPNS crawl, lookups wired up |
| [Overlay Storage Isolation](./overlay-storage-isolation.md) | **Complete** | Per-topic SQLite, TxTopicIndex, dead code removed |
| [OrdLock Overlay](./ordlock-overlay.md) | **Complete** | Deployed to rack, syncing from block 783968 |
| [TXO Lookup Fixes](./TXO_LOOKUP_FIXES.md) | **Complete** | LoadOutputsByTxid fixed to use event index |

## Status Legend

- **Not Started**: Plan created, work not begun
- **In Progress**: Active development
- **BLOCKED**: Waiting on dependency or issue resolution
- **Complete**: Work finished and verified
