# Project Plans

| Plan | Status | Description |
|------|--------|-------------|
| [Admin & OpNS Registration](./2026-03-03-admin-opns-registration.md) | **In Progress** | Admin setup working, OPNS crawl complete, lookups wired up |
| [Overlay Storage Isolation](./overlay-storage-isolation.md) | **Complete** | All 10 steps done — per-topic SQLite, TxTopicIndex, dead code removed |
| [OrdLock Overlay](./ordlock-overlay.md) | **Complete** | Deployed to rack, syncing from block 783968 |
| [TXO Lookup Fixes](./TXO_LOOKUP_FIXES.md) | **In Progress** | LoadOutputsByTxid scans wrong key space, direct outpoint lookup |
| [Event-Driven Overlay Routing](./event-driven-overlay-routing.md) | **In Progress** | PubSub patterns, EventBridge, parser events, wiring complete — needs deploy |

## Completed Plans

| Plan | Status | Description |
|------|--------|-------------|
| Wallet Connect Flow | **COMPLETE** | yours-wallet auth and popup fixes done |

## Status Legend

- **Not Started**: Plan created, work not begun
- **In Progress**: Active development
- **BLOCKED**: Waiting on dependency or issue resolution
- **Complete**: Work finished and verified
