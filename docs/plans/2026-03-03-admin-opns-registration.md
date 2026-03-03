# OpNS Name Publishing: SDK + Server Changes

## Context

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
