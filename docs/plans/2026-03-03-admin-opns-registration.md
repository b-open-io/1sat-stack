# OpNS Name Publishing: SDK + Server Changes

**Status: BLOCKED** 🔴 — Admin setup flow not working

---

## Current Blocker

The "Confirm Admin Identity" button in the admin UI does not make a network request. This blocks admin setup, which is required for admin permissions to publish OpNS names.

**Symptoms**:
- Button text changes to "Configuring..." and disables
- No network request appears in browser dev tools
- Likely JavaScript error or auth initialization issue

**Decision needed**: Fix admin setup first, or skip to OpNS/Paymail testing without admin.

---

## Prerequisites Completed ✅

### Wallet Extension (yours-wallet)
All wallet authentication and popup issues resolved:
- Auth flow fixed: `verifyAccess()` checks `isLocked`, `lockWallet()` doesn't remove `passKey`
- Popup behavior fixed: No double popups, proper close after unlock
- Session sharing: Wallet and server share proper auth session

### Server Auth & Routes (1sat-stack)
- Setup routes moved to `/admin/setup/*` to avoid AdminGuard
- Auth middleware refactored for proper HTTP-layer composition
- Wallet routes use `HTTPHandler()` for correct auth context flow

### Current Status
Wallet connects successfully. Admin UI shows "Connected" with identity key.
But **"Confirm Admin Identity" button doesn't trigger network request**.

---

## Original Plan

The admin UI is a React + TypeScript SPA with BRC-103/104 wallet auth and API key fallback. The 1sat-sdk has `opnsRegister`/`opnsDeregister` actions for publishing names. The server has paymail, OpNS overlay, and OrdFS services.

Before we can publish OpNS names from the admin UI, we need consistent ordinal handling across the SDK and server. Currently, the SDK's sweep and transfer actions hardcode all ordinals into the `1sat` basket regardless of content type, and the paymail receive endpoint treats all incoming outputs as wallet payments.

## Work Items

### 1. SDK: Content-type-aware basket routing in `sweepOrdinals`

**File:** `1sat-sdk/packages/actions/src/sweep/index.ts`

Currently hardcodes `basket: ORDINALS_BASKET` (`'1sat'`) for all ordinals (line 453). The caller already provides `contentType`, `origin`, and `name` per input.

Changes:
- Reject `application/bsv-20` inputs with an error (sweeping tokens through ordinal path burns them)
- Route `application/op-ns` to basket `opns`
- Default to basket `1sat` for everything else
- Extensible — easy to add more content-type routings later

### 2. SDK: Content-type guard in `buildTransferOrdinals`

**File:** `1sat-sdk/packages/actions/src/ordinals/index.ts`

Currently hardcodes `basket: ORDINALS_BASKET` (line 293). Source output tags include `type:{contentType}`.

Changes:
- Reject transfers where source output has `type:application/bsv-20` tag (prevents token burns)
- Preserve existing basket from source output rather than hardcoding (if source was in `opns` basket, keep it there)
- Note: this requires the source `WalletOutput` to carry basket info — need to verify what `listOutputs` returns

### 3. Server: Ordinal-aware paymail receive

**File:** `1sat-stack/pkg/paymail/routes.go` (`internalizePayment` at line 349)

Currently internalizes everything as `InternalizeProtocolWalletPayment`. The paymail service already has `ordfs` and `opns` dependencies wired in.

Changes:
- Detect 1-sat outputs in the received transaction
- For 1-sat outputs, look up origin + metadata from OrdFS
- Route by content type:
  - `application/op-ns` → `InternalizeProtocolBasketInsertion` with basket `opns`, tags from OrdFS (`origin:`, `type:`, `name:`)
  - `application/bsv-20` → placeholder/TODO for BSV-21 overlay submission (don't wire yet, just note it)
  - Other 1-sat → `InternalizeProtocolBasketInsertion` with basket `1sat`, tags from OrdFS
- `> 1 sat` → existing `wallet payment` path unchanged
- Use `BasketInsertion` struct from go-sdk (`Basket`, `CustomInstructions`, `Tags`)
- `customInstructions` still carries BRC-29 derivation info for spending

### 4. SDK: Add `OPNS_BASKET` constant

**File:** `1sat-sdk/packages/types/src/constants.ts`

Add `OPNS_BASKET = 'opns'` alongside existing `ORDINALS_BASKET`, `BSV21_BASKET`, `LOCK_BASKET`.

## Deferred

- **BSV-21 paymail routing** — needs overlay submission, not just basket insertion. Noted in code, wired later.
- **OpNS name publishing from admin UI** — depends on names being in the wallet with correct baskets first
- **OpNS mining as a paid service** — server-side PoW, deferred until publish flow works
- **OrdFS lookup in sweep** — caller is expected to provide metadata; sweep trusts input data

## Key Files

### SDK (1sat-sdk)
- `packages/actions/src/sweep/index.ts` — sweep ordinals action
- `packages/actions/src/ordinals/index.ts` — transfer ordinals action
- `packages/actions/src/opns/index.ts` — opnsRegister/opnsDeregister actions
- `packages/types/src/constants.ts` — basket/protocol constants
- `packages/wallet/src/indexers/OpNSIndexer.ts` — wallet indexer (address sync path)

### Server (1sat-stack)
- `pkg/paymail/routes.go` — paymail receive endpoints + internalizePayment
- `pkg/paymail/service.go` — paymail service with OrdFS + OpNS dependencies
- `pkg/ordfs/` — OrdFS client for origin/metadata lookups
- `pkg/opns/lookup.go` — OpNS overlay lookup service

### Go SDK (go-sdk)
- `wallet/interfaces.go` — `BasketInsertion`, `InternalizeProtocol`, `InternalizeOutput`

---

## Next Steps

### Option 1: Fix Admin Setup First (Recommended)
1. Debug why `performSetup()` in `admin/ui/src/api.ts` doesn't make the POST request
2. Check browser console for JavaScript errors
3. Verify `authFetch` is initialized after wallet connect
4. Fix the issue and complete admin setup
5. Then proceed with OpNS work items above

### Option 2: Skip to OpNS Testing
1. Test OpNS lookup functionality (read-only, doesn't need admin)
2. Test Paymail receive with existing wallet
3. Return to admin setup later when write operations are needed

**Note**: Admin permissions may be required for some OpNS management operations.

---

## Debugging the Admin Setup Issue

**Files to check**:
- `admin/ui/src/api.ts:55-67` — `performSetup()` function
- `admin/ui/src/sections/SetupWizard.tsx:13-24` — `handleSetup()` handler
- Browser console for JavaScript errors
- Network tab to confirm no request is made

**Likely causes**:
- `authFetch` not initialized (should be set in `connectWallet()`)
- JavaScript error before fetch call
- Wrong URL construction (`${SETUP_BASE}/setup` = `/admin/setup/setup` but endpoint is `/admin/setup`)
