# Market API & OPNS Validation

Status: **In Progress**

## Summary

Rename ordlock routes to `/market`, fix origin storage from TEXT to BLOB, add bulk OPNS origin validation, create SDK clients, and wire up 1sat-name marketplace.

## Changes

### 1sat-stack (DONE)

- [x] Rename ordlock routes `/ordlock` → `/market` (external paths, swagger, capability string)
- [x] Default prefix in `pkg/ordlock/config.go`
- [x] Fallback prefix and capability in `cmd/server/config.go`
- [x] Swaggo `@Tags` and `@Router` annotations in `pkg/ordlock/routes.go`
- [x] Fix ordlock `origin` TEXT → BLOB in `pkg/lookup/ordlock.go`
  - Schema changed
  - `OutputAdmittedByTopic` stores `origin.Bytes()` instead of `OrdinalString()`
  - `scanListing` reads binary, converts to string at API boundary
  - `GetListingByOrigin` accepts `*transaction.Outpoint`
  - `GetListingsByOrigins` accepts `[]*transaction.Outpoint`
  - Route handlers parse incoming strings via `OutpointFromString`
- [x] Fix paymail `txid` TEXT → BLOB in `pkg/paymail/store_sqlite.go` and `store.go`
  - Routes store `txid[:]` instead of `txid.String()`
- [x] Add `POST /opns/origins` bulk validation in `pkg/opns/routes.go` and `pkg/opns/lookup.go`
  - `ValidateOrigins` uses `db.FindOutputs` with binary outpoints
  - Route parses strings via `OutpointFromString`, returns `map[string]bool`
- [x] Run `./build-docs.sh` to regenerate swagger with new routes/tags
- [x] Evaluate swagger output for any missing client coverage

### 1sat-sdk (DONE)

- [x] Create `MarketClient` in `packages/client/src/services/MarketClient.ts`
  - `searchListings`, `getListing`, `getListingByOrigin`, `getListingsByOrigins`
- [x] Create `OpnsClient` in `packages/client/src/services/OpnsClient.ts`
  - `getOrigin`, `getMine`, `validateOrigins`
- [x] Export from `services/index.ts`
- [x] Add `market` and `opns` to `OneSatServices`
- [x] Evaluate swagger for any other APIs missing client coverage — see gaps below
- [ ] Publish new `@1sat/client` version

#### SDK Client Coverage Gaps (separate workstream)

Routes in swagger without SDK clients:
- **BAP** (4 routes) — identity/get, identity/search, profile, profile/{bapId}
- **BSocial** (1 route) — post/search
- **Paymail** (5 routes) — bsvalias capability, p2p destination, receive-beef, receive-transaction
- **Overlay admin** (7 routes) — startGASPSync, syncAdvertisements, documentation, requestForeignGASPNode, requestSyncResponse, lookup
- **System** (2 routes) — /capabilities, /health
- **SSE** (1 route) — /sse/{topics}
- **Admin partial** (4 routes) — progress update/delete, bsv21/workers, topics remotes CRUD

### 1sat-name (IN PROGRESS)

- [x] Update `MarketplacePage.tsx` to use `/market/listings?type=application/op-ns`
- [x] Add origin validation via `POST /opns/origins`
- [ ] Switch from raw `apiFetch` to `MarketClient` and `OpnsClient` from SDK
- [ ] Test against live server

### Deploy (NOT STARTED)

- [ ] Commit and push 1sat-stack changes
- [ ] SSH to rack, pull, build
- [ ] Delete ordlock overlay DB on rack
- [ ] Delete paymail DB on rack
- [ ] Reset ordlock topic progress
- [ ] Restart PM2 — will re-index with correct schema
- [ ] Verify `/market/listings` and `/opns/origins` work
