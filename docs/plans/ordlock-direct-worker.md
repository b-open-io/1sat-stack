# OrdLock Direct Worker (Remove Overlay Engine)

Status: **Not Started**

## Summary

Replace the overlay engine pipeline for OrdLock with a direct worker that reads from the queue and writes to a standalone SQLite listings table. OrdLock doesn't need graph integrity or GASP sync — it's a flat set of independent listings.

## Why

- OrdLock listings are independent (no dependency graph like OPNS/BSV21)
- GASP sync was failing because `processWithGASP` blindly iterated all outputs without pre-filtering
- The overlay engine adds unnecessary complexity (topic manager, lookup service interface, GASP, CoinsToRetain ordering concerns)
- Parallel processing with GASP required ordering guarantees that aren't needed with proper upserts

## Changes

### Step 1: New ordlock worker handler

Create a direct handler in `pkg/ordlock/` (e.g. `processor.go`) that:
- Takes a txid from the queue
- Loads BEEF via `beefStorage.BuildFullBeef` or `LoadTx`
- Scans outputs: if `ordlock.Decode(output.LockingScript) != nil`, upsert listing creation
- Scans inputs: if `ordlock.Decode(sourceOutput.LockingScript) != nil`, upsert listing spend
- Uses `extractListingData` (already written in `pkg/lookup/ordlock.go`) for both paths
- Uses `classifySpend` for sale vs cancel detection
- Gets listing score from the listing tx's MerklePath, spend score from the spending tx's MerklePath

### Step 2: Own SQLite database

- OrdLock gets its own SQLite DB (not the overlay topic DB factory)
- Same schema as current `listings` table
- `OrdLockLookup` changes from `topicDB overlaystorage.Factory` to a direct `*sql.DB`
- Remove `topic string` parameter from all query methods (always one DB)
- Remove `db(topic string)` method, replace with direct DB access

### Step 3: Remove overlay interface methods from OrdLockLookup

From `pkg/lookup/ordlock.go`, remove:
- `OutputAdmittedByTopic` — replaced by direct upsert in the worker
- `OutputSpent` — replaced by direct upsert in the worker
- `OutputNoLongerRetainedInHistory`
- `OutputEvicted`
- `OutputBlockHeightUpdated`
- `Lookup`
- `GetDocumentation`
- `GetMetaData`

Keep:
- `extractListingData` (move to `pkg/ordlock/` or keep here, used by worker)
- `SearchListings`, `GetListing`, `GetListingByOrigin`, `GetListingsByOrigins`
- `scanListing`
- `ordlockSchema`
- ORDFS integration for origin resolution on transferred ordinals

### Step 4: Remove topic manager

Delete `pkg/ordlock/topic.go` entirely.

### Step 5: Update config.go and Services struct

`pkg/ordlock/config.go`:
- Remove `TopicManager` from `Services` struct
- Change `Sync *overlay.OverlaySync` to `Worker *worker.Worker` (or a custom processor type)
- `Initialize` takes BEEF storage + store + logger instead of `overlaystorage.Factory`
- Creates own SQLite DB
- Remove `overlay` and `overlaystorage` imports

`cmd/server/config.go` — remove:
- `svc.Overlay.RegisterLookupService("ordlock", ...)` (line 640)
- Topic activation block (lines 642-650)
- `overlay.NewOverlaySync(...)` (line 660)
- `svc.OrdLock.Sync.Start(ctx)` (lines 1536-1542)

`cmd/server/config.go` — change:
- OrdLock initialization (lines 631-663): no overlay dependency, create own DB, create worker
- Event bridge (lines 1451-1464): keep as-is (still feeds q:ordlock queue)
- JungleBus subscriber (lines 936-944): keep as-is
- Worker start: launch new direct worker instead of overlay sync
- ORDFS wiring (lines 680-682): keep, still needed for origin resolution

`cmd/server/config.go` — keep:
- Route registration (lines 1068-1075)

### Step 6: Upsert SQL (already written)

Creation upsert:
```sql
INSERT INTO listings (outpoint, origin, name, content_type, price, seller, score)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(outpoint) DO UPDATE SET
  origin = excluded.origin, name = excluded.name,
  content_type = excluded.content_type, price = excluded.price,
  seller = excluded.seller, score = excluded.score
```

Spend upsert:
```sql
INSERT INTO listings (outpoint, origin, name, content_type, price, seller, score, spend_txid, spend_type, spend_score)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(outpoint) DO UPDATE SET
  origin = excluded.origin, name = excluded.name,
  content_type = excluded.content_type, price = excluded.price,
  seller = excluded.seller,
  spend_txid = excluded.spend_txid, spend_type = excluded.spend_type,
  spend_score = excluded.spend_score
```

Both paths fully populate all fields since the BEEF contains ancestor transactions.

### Step 7: Deploy to rack

- Delete ordlock overlay DB on rack (`~/.1sat/overlay/topic_tm_ordlock.db`)
- Update config.yaml: remove `resolve_dependencies`, keep queue/sync settings
- Rebuild and restart PM2
- Reset ordlock JungleBus subscription progress to re-crawl
- Verify listings populate

### Step 8: Cleanup

- Remove debug logs from `pkg/worker/worker.go` (deployed to rack via scp)
- Remove `OrdinalOutput`/`OrdinalInput` from `pkg/types/ordinal.go` if not used by other packages (keep if OPNS or future work needs them)
- Run `go vet ./...` and `gofmt -s -w .`

## Files affected

| File | Action |
|------|--------|
| `pkg/ordlock/topic.go` | Delete |
| `pkg/ordlock/processor.go` | Create (new worker handler) |
| `pkg/ordlock/config.go` | Rewrite (remove overlay deps, own DB, worker) |
| `pkg/ordlock/routes.go` | Minor (remove topic param if needed) |
| `pkg/lookup/ordlock.go` | Simplify (remove overlay interface, own DB, keep queries) |
| `cmd/server/config.go` | Update wiring |
| `pkg/worker/worker.go` | Remove debug logs |
| `pkg/types/ordinal.go` | Keep (may be useful for OPNS later) |

## Not in scope

- OPNS GASP sync issues (needs its own investigation — dependency graph is real)
- BSV21 changes (working correctly with discovery topic pre-filtering)
- Renaming `X-Api-Key` to `Authorization: Bearer`
