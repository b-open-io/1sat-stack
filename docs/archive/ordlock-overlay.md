# OrdLock Overlay

Status: **Complete**

## Summary

Add OrdLock marketplace overlay to 1sat-stack. Indexes listing creation (OrdLock script outputs) and spend events (sales/cancels) into a per-topic SQLite database via the overlay engine.

## What's Done

- [x] Topic manager (`pkg/ordlock/topic.go`) — admits OrdLock outputs, tracks spends via previousCoins, returns MissingInputError for unresolved inputs
- [x] Lookup service (`pkg/lookup/ordlock.go`) — custom `listings` table with origin, name, content_type, price, seller, spend tracking. Classifies spends as sale/cancel from unlocking script branch selector
- [x] Routes (`pkg/ordlock/routes.go`) — `GET /ordlock/listings` (search with filters), `GET /ordlock/listing/:outpoint`
- [x] Config (`pkg/ordlock/config.go`) — standard mode/sync/routes pattern
- [x] Server wiring (`cmd/server/config.go`) — topic activation, lookup registration, JungleBus subscriber, sync worker startup, shutdown
- [x] Unified MissingInputError (`pkg/overlay/errors.go`) — shared by BSV21, OPNS, OrdLock
- [x] MissingInputError handling in sync worker (`pkg/overlay/sync.go`) — skip after GASP resolution fails
- [x] GASP dependency resolution in OverlaySync (`resolve_dependencies` config flag) — uses BeefRemote + ProcessUTXOToCompletion for overlays with UTXO chains
- [x] Config files updated (`config.yaml`, `config.example.yaml`) — ordlock section, tm_ordlock in topic_whitelist

## Deploy Steps (Rack) — Completed 2026-03-12

1. Pushed `overlay-storage-isolation` branch to origin
2. Stopped PM2 (`pm2 delete stack`)
3. Pulled code on rack
4. Updated PM2 config: moved subscription ID `9efa781...` from `ONESAT_INDEXER_SYNC_SUBSCRIPTION_IDS` to `ONESAT_ORDLOCK_SYNC_SUBSCRIPTION_ID`
5. Added `ONESAT_AUTH_API_KEY` to PM2 config (for admin API access via `X-Api-Key` header)
6. Added ordlock section to `config.yaml` on rack (mode: embedded, sync enabled, resolve_dependencies: true)
7. Added `tm_ordlock` to overlay topic_whitelist in `config.yaml`
8. Built admin UI and Go binary on rack
9. Started PM2 from ecosystem file (`pm2 start ~/Code/pm2/stack.config.js`)
10. Deleted progress via admin API (`DELETE /admin/api/progress/{sub_id}` with `X-Api-Key` header)
11. Restarted PM2 to pick up cleared progress — OrdLock subscriber syncing from block 783968

## Architecture Notes

- OrdLock uses `OverlaySync` with `resolve_dependencies: true` — GASP walks input graph via BeefRemote before submitting
- Overlays without UTXO chains (BAP, BSocial) use `resolve_dependencies: false` (direct submit)
- BSV21 has its own custom sync (dispatcher + per-token TopicWorkers) — separate from OverlaySync
- OPNS has genesis crawl — separate from OverlaySync
- JungleBus subscriber feeds `q:ordlock`, OverlaySync consumes it
- Data goes to `~/.1sat/store_tm_ordlock.db` (per-topic SQLite)
- No store clear needed — overlay data is separate from Badger store
