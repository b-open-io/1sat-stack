# OrdLock Overlay

Status: **In Progress**

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

## Deploy Steps (Rack)

1. Delete progress for the OrdLock subscription ID via admin API (`DELETE /admin/progress/{sub_id}`)
2. Stop PM2 (`pm2 delete stack`)
3. Pull code on rack
4. Update PM2 config: move subscription ID to `ONESAT_ORDLOCK_SYNC_SUBSCRIPTION_ID`
5. Build: `cd admin/ui && bun run build && cd ../.. && go build -o server ./cmd/server`
6. Start: `pm2 start ~/Code/pm2/stack.config.js`

Progress must be deleted before starting PM2 (or before stopping the old server via admin API), because the subscriber reads progress once at startup and never re-checks.

## Architecture Notes

- OrdLock uses `OverlaySync` with `resolve_dependencies: true` — GASP walks input graph via BeefRemote before submitting
- Overlays without UTXO chains (BAP, BSocial) use `resolve_dependencies: false` (direct submit)
- BSV21 has its own custom sync (dispatcher + per-token TopicWorkers) — separate from OverlaySync
- OPNS has genesis crawl — separate from OverlaySync
- JungleBus subscriber feeds `q:ordlock`, OverlaySync consumes it
- Data goes to `~/.1sat/store_tm_ordlock.db` (per-topic SQLite)
- No store clear needed — overlay data is separate from Badger store
