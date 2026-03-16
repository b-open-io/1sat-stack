# BAP API Consolidation — Gap Analysis & Execution Plan

Status: **Complete**

Epic: OPL-1110

## Overview

Consolidating BAP identity API endpoints into 1sat-stack, replacing bsocial-overlay (`api.sigmaidentity.com`). sigma-auth is the primary consumer.

## Porting Status

The BAP data layer (types.go, lookup.go) was ported correctly from bsocial-overlay. Types are identical, all LookupService methods are present. The gap is in the HTTP routes layer — bsocial-overlay had 7 endpoints, only 4 were wired up in 1sat-stack.

---

## Backup Format → BRC-100 Root → BAP ID Mapping

All backup formats ultimately resolve to a single BRC-100 wallet root key, from which BAP identity keys are derived via Type42.

| Format | Key Fields | BRC-100 Root Key | BAP ID Derivation | Status |
|--------|-----------|-----------------|-------------------|--------|
| **BapMasterBackupLegacy** | `xprv`, `mnemonic`, `ids` | `BAP(xprv)` | `bap.listIds()[0]` from `ids` | Working |
| **MasterBackupType42** | `rootPk` (WIF), `ids` | `rootPk` is the root | `bap.listIds()[0]` from `ids` | Working |
| **BapMemberBackup** (now "account") | `wif`, `id` | `wif` is the member/account key | `id` is the BAP ID directly | Embedded in master backups |
| **WifBackup** | `wif` | `wif` becomes root | Same derivation as other formats | Working |
| **OneSatBackup** | `ordPk`, `payPk`, `identityPk` | `identityPk` is the root | Derive BAP ID from `identityPk` | OPL-1116: derive `identityPk` from other keys when missing |
| **YoursWalletBackup** | `payPk`, `ordPk`, `identityPk`, `mnemonic?` | `identityPk` is the root | Same as OneSatBackup | OPL-1116: same derivation needed |
| **YoursWalletZipBackup** | `chromeStorage`, `accountData` | N/A | N/A | Deprecated — OPL-1117: new format later |
| **VaultBackup** | `encryptedVault`, `scheme` | TBD | TBD | Deferred — more complexity, no clear mapping yet |

`prepareBackupForSignIn` in `auth-flows.ts` only handles Legacy and Type42 today. The other formats fall through without a bapId unless the caller provides one explicitly. OPL-1116 addresses the OneSat/YoursWallet gap.

---

## Sign-In Call Chain (actual execution)

```
1. Client app (1sat-website, Scribe, etc.)
   └─ authClient.signIn.sigma({ clientId }) → OAuth redirect to sigma-auth

2. sigma-auth — authorize page
   └─ User picks identity → signer iframe gets SET_IDENTITY(bapId)
   └─ User signs request with wallet root key (memberBackup.wif)
   └─ Calls: $fetch("/sign-in/sigma", { body: {}, headers: { "X-Auth-Token": authToken } })
                                               ^^^ BUG: bapId not sent

3. better-auth-plugin — signInSigma (src/provider/index.ts:690)
   └─ Extracts pubkey from x-auth-token (this is the wallet root pubkey)
   └─ Finds/creates user by pubkey
   └─ Calls: options.resolveBAPId(pool, userId, pubkey, true)

4. sigma-auth — resolvePubkeyAndRegisterBAPId (lib/bap/resolver.ts:732)
   └─ resolvePubkeyToBapId(pubkey)
       └─ KV cache miss (first sign-in)
       └─ resolvePubkeyViaIndexer(pubkey) → GET /resolve/{pubkey} → 404 (never existed)
       └─ Returns null

5. Back in signInSigma: bapId is null → profile update block skipped
   └─ Session created without BAP identity data
```

## Key Discovery: Wallet Root vs Identity Keys

The wallet root pubkey (from `memberBackup.wif`) is NOT an on-chain identity key. BAP identities use derived `identity-{N}` keys via Type42. The overlay only indexes on-chain data — it has no knowledge of which wallet root key owns which identity. This mapping exists only in sigma-auth's database (`profile.member_pubkey`).

```
Wallet Root (memberBackup.wif)  ← signs auth token, NOT on-chain
└── Type42 derive: protocolID=[1,"sigma"], keyID="identity-0"
    └── identity-0 pubkey → address → bapIdFromAddress() → BAP ID
        └── This address IS in the BAP identity's addresses[] array
```

A `/resolve/{pubkey}` endpoint doing pubkey→address→LoadIdentityByAddress would never find anything because the wallet root address is not in any identity's address list.

---

## Issue Status

| Issue | Status | Description |
|-------|--------|-------------|
| OPL-1115 | **Cancelled** | Not a bug — bapId IS sent in request body. Dead code cleaned up in OPL-1113. |
| OPL-1112 | **Done** | `POST /identity/validByAddress` added to 1sat-stack |
| OPL-1113 | **Done** | sigma-auth repointed to 1sat-stack for all BAP lookups |
| OPL-1111 | **Cancelled** | `/resolve/{pubkey}` can't work — wallet root pubkey isn't on-chain |

## Execution Order

### Step 1: OPL-1115 — Fix sign-in bapId passthrough (sigma-auth)

The authorize/login page in sigma-auth already knows the `bapId` (sends it to signer via `SET_IDENTITY`). It needs to include it in the `POST /sign-in/sigma` request body.

The `signInSigma` endpoint in better-auth-plugin already reads `ctx.body?.bapId` (line 841) and uses it to query the correct profile. No plugin changes needed.

**Change**: Find where sigma-auth calls `signIn.sigma({ authToken })` and add the bapId: `signIn.sigma({ authToken, bapId })`.

### Step 2: OPL-1112 — Add validByAddress endpoint (1sat-stack)

Add `POST /1sat/bap/identity/validByAddress` for key rotation validation.

- Parse `{ address, block?, timestamp? }`
- `LoadIdentityByAddress(ctx, address)` — already exists in lookup.go
- Walk `identity.Addresses` by block or timestamp to determine validity
- Return response matching bsocial-overlay's shape
- Reference: bsocial-overlay server.go:411-500

### Step 3: Response format decision

Existing 1sat-stack endpoints return data directly. bsocial-overlay wraps in `{ status: "OK", result: ... }`. Need to decide which way to go before OPL-1113.

### Step 4: OPL-1113 — Repoint sigma-auth URLs (sigma-auth)

- Add `BAP_OVERLAY_URL` env var
- Replace hardcoded `api.sigmaidentity.com` in resolver.ts, api.ts, server.ts
- Normalize path construction for 1sat-stack routes
- Clean up `resolvePubkeyViaIndexer` (dead code calling nonexistent endpoint)

### Step 5: Deploy and verify

- Deploy 1sat-stack with new endpoint
- Deploy sigma-auth with bapId fix and repointed URLs
- Verify first-time sign-in flow end-to-end

---

## API Surface Reference

### sigma-auth external BAP calls

| Endpoint | Method | Caller | Status |
|----------|--------|--------|--------|
| `/resolve/{pubkey}` | GET | `resolvePubkeyViaIndexer()` | Dead — never existed. Fix via OPL-1115. |
| `/v1/identity/validByAddress` | POST | `getBapIdFromPubkey()` | Works. Needs 1sat-stack equivalent (OPL-1112). |
| `/api/v1/identity/get` | POST | `fetchFromChainAPI()` | Works. Already in 1sat-stack. |
| `/v1/identity/get` | POST | `fetchBAPProfileByPubkey()` | Duplicate of above with wrong prefix. |

### 1sat-stack current BAP routes (`/1sat/bap/`)

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/identity/get` | POST | Identity by idKey |
| `/identity/search` | GET | Full-text search |
| `/profile` | GET | List profiles |
| `/profile/:bapId` | GET | Profile by BAP ID |

### Missing from 1sat-stack

| Endpoint | Method | Ticket | Notes |
|----------|--------|--------|-------|
| `/identity/validByAddress` | POST | OPL-1112 | Key rotation validation |
| `/person/:field/:bapId` | GET | None | Low priority, no sigma-auth callers |
| `/autofill` | GET | None | Low priority, no known callers |

## Resolved Decisions

1. **Response wrapper**: sigma-auth updated to handle 1sat-stack's direct response format.
2. **Default for validByAddress with no block/timestamp**: Checks `CurrentAddress` equality.
3. **Backward compat**: Hard cutover — sigma-auth now points directly at 1sat-stack.
