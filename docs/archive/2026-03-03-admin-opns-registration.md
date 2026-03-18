# OpNS Name Publishing: SDK + Server Changes

**Status: In Progress**

---

## Completed ✅

### Admin Setup
- Admin setup flow working end-to-end (wallet connect → confirm identity → admin UI)
- BRC-103/104 mutual authentication working between admin UI and server
- Setup routes at `/admin/setup/*`, auth middleware properly composed

### OPNS Genesis Crawl
- Crawl triggered from admin UI "Sync OpNS Tree" button
- 14,058 OPNS names processed and stored
- Data stored in Badger with `tm_opns:name:{name}` and `tm_opns:mine:{prefix}` keys

### OPNS Lookup Endpoints
- `GET /opns/origin/:name` — returns outpoint for registered name (or 404)
- `GET /opns/mine/:name` — returns longest mined prefix outpoint (or 404 if taken/not found)
- Both wired into admin UI with dedicated cards

### Server Deployment
- Running on rack via PM2 ecosystem file with correct env vars
- BSV21/BAP/BSocial sync subscription IDs commented out (not needed yet)
- Arcade running for merkle proof fetching

---

## Known Issues

### Session Persistence (TODO)
Server uses in-memory `SessionManager`. BRC-103/104 sessions are lost on server restart. Workaround: reload Yours Wallet extension to force fresh handshake. Need Badger or Redis-backed session store.

### CWI Intermittent Hang (yours-wallet)
CWI calls from dApp pages intermittently hang after page reload without extension reload. Diagnostic logging added to background.ts handlers. See `yours-wallet/docs/plans/2026-03-04-cwi-intermittent-hang.md`.

---

## Next Steps

### 1. Sweep OPNS name into BRC-100 wallet
OPNS names are currently owned by wallets not present in BRC-100. Need to sweep at least one name into the BRC-100 wallet to test paymail functionality. This requires the sweep UI in admin or an external tool.

### 2. SDK: Content-type-aware basket routing in `sweepOrdinals`

**File:** `1sat-sdk/packages/actions/src/sweep/index.ts`

Currently hardcodes `basket: ORDINALS_BASKET` (`'1sat'`) for all ordinals. Changes:
- Reject `application/bsv-20` inputs (sweeping tokens burns them)
- Route `application/op-ns` to basket `opns`
- Default to basket `1sat` for everything else

### 3. Server: Ordinal-aware paymail receive

**File:** `1sat-stack/pkg/paymail/routes.go`

Currently internalizes everything as wallet payment. Changes:
- Detect 1-sat outputs, look up origin + metadata from OrdFS
- Route by content type to appropriate basket insertion
- `application/op-ns` → basket `opns` with tags

### 4. SDK: Add `OPNS_BASKET` constant

**File:** `1sat-sdk/packages/types/src/constants.ts`

Add `OPNS_BASKET = 'opns'` alongside existing constants.

---

## Key Files

### Server (1sat-stack)
- `pkg/opns/routes.go` — OPNS HTTP endpoints (origin, mine)
- `pkg/opns/lookup.go` — OPNS lookup service (Origin, Mine methods)
- `pkg/opns/crawl.go` — Genesis crawl implementation
- `pkg/paymail/routes.go` — paymail receive endpoints
- `admin/routes.go` — admin API routes (crawl trigger)
- `admin/ui/src/pages/OpNSPage.tsx` — admin OPNS page

### SDK (1sat-sdk)
- `packages/actions/src/sweep/index.ts` — sweep ordinals action
- `packages/actions/src/ordinals/index.ts` — transfer ordinals action
- `packages/actions/src/opns/index.ts` — opnsRegister/opnsDeregister actions
- `packages/types/src/constants.ts` — basket/protocol constants
