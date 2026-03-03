# Payment Receiving via OpNS Names for 1sat-stack

## Context

We need external users to send payments into BRC-100 wallets hosted in 1sat-stack. The approach:
- **OpNS names** serve as human-readable aliases (e.g., `alice`)
- **MAP metadata on OpNS tokens** provides the identity binding — the token's MAP `opns.idKey` field contains the wallet's identity public key
- **BRC-29 address derivation** with the "anyone" counterparty generates unique payment addresses server-side using only the public key
- **Delivery** can use Paymail (BRC-28), MessageBox (BRC-31), or both — the delivery mechanism is independent of the identity resolution

### How It Works (DNS Analogy)

OpNS works like DNS. The OpNS token is the zone record. MAP fields are the record data. Re-inscribing updates the record. ORDFS serves the latest state.

```
alice (OpNS token)
  └── MAP: { "opns.idKey": "03abc..." }
                              └── identity public key
```

To resolve a name for payment:
1. Look up `alice` via ORDFS/overlay → get the OpNS token
2. Read the MAP `opns.idKey` field → that's the BRC-100 identity public key
3. Use identity key for BRC-29 payment derivation

### Key Design Decisions

1. **OpNS is the only alias source** — no database-backed alias registration
2. **Identity binding is on-chain via MAP metadata** — no special derivation scheme, no P2PK scripts, no server-side registration required for the binding itself
3. **OpNS tokens use standard P2PKH** — the token sits at a normal ordinals address in the wallet's existing `ordinals` basket
4. **Payment derivation uses the identity key from MAP** (not the locking script address) — the MAP `opns.idKey` field IS the identity
5. **Activation = re-inscribe with MAP data** — the user transfers the OpNS ordinal to self with MAP metadata declaring the identity key, then submits to the OpNS overlay
6. **Deactivation = re-inscribe without MAP data** (or transfer away) — removes the identity binding
7. **Delivery protocol is orthogonal** — paymail and/or messagebox can both use the same identity resolution

### BRC-29 Payment Address Derivation (settled)

```
// Server (or sender) generates random prefix/suffix per payment:
protocol      = [2, "3241645161d8"]  // BRC-29
keyID         = base64(randomPrefix) + " " + base64(randomSuffix)
invoiceNumber = "2-3241645161d8-" + keyID

anyonePrivKey, _ = wallet.AnyoneKey()
paymentPubKey, _ = identityPubKey.DeriveChild(anyonePrivKey, invoiceNumber)
address          = P2PKH(paymentPubKey)
```

Verified: works with anyone counterparty because `identityPubKey * anyonePrivKey(1) == identityPubKey`, and the wallet can derive the corresponding private key via `identityPrivKey.DeriveChild(anyonePubKey, invoiceNumber)`.

---

## Implementation Plan

### Step 1: Add MAP Support to transferOrdinals (1sat-sdk)

**File:** `1sat-sdk/packages/actions/src/ordinals/index.ts`

Add optional `map` field to `TransferItem`:

```typescript
export interface TransferItem {
  ordinal: WalletOutput
  counterparty?: PubKeyHex
  address?: string
  map?: Record<string, string>  // optional MAP metadata to append
}
```

In `buildTransferOrdinals`, when `map` is provided, append MAP data to the output script using `appendMapToScript` from `@1sat/core/map`. The output script becomes `<P2PKH> OP_RETURN <MAP SET ...>` instead of just `<P2PKH>`.

**Reference files:**
- `1sat-sdk/packages/core/src/map/index.ts` — `appendMapToScript()`, `buildMapAsm()`
- `1sat-sdk/packages/actions/src/inscriptions/index.ts` — pattern for building combined scripts

### Step 2: OpNS Register Action (1sat-sdk)

**New file:** `1sat-sdk/packages/actions/src/opns/index.ts`

An `opnsRegister` action that:
1. Gets the wallet's identity public key
2. Transfers the OpNS ordinal to self with MAP data: `{ 'opns.idKey': identityPubKeyHex }`
3. After signing, submits the transaction to the OpNS overlay topic

```typescript
// Pseudocode
const { publicKey: identityPubKey } = await ctx.wallet.getPublicKey({
  identityKey: true,
})

// Transfer to self with MAP
const params = await buildTransferOrdinals(ctx, {
  transfers: [{
    ordinal,
    counterparty: 'self',  // transfer to self
    map: { 'opns.idKey': identityPubKey }
  }],
  inputBEEF,
})

// ... sign ...

// Submit to OpNS overlay
await ctx.services.overlay.submit(signResult.tx, ['tm_opns'])
```

An `opnsDeregister` action would transfer to self with MAP data `{ 'opns.idKey': '' }` to explicitly clear the binding.

**Reference files:**
- `1sat-sdk/packages/actions/src/tokens/index.ts` — BSV21 overlay submission pattern
- `1sat-sdk/packages/client/src/services/OverlayClient.ts` — `submit(beef, topics)` method

### Step 3: OpNS Indexer — Index MAP opns.idKey

**File:** `1sat-stack/pkg/opns/lookup.go`

Update `OutputAdmittedByTopic()` to detect and store the MAP `opns.idKey` field when present on OpNS outputs.

1. After detecting an OpNS inscription, parse MAP data from the output script
2. If MAP contains `opns.idKey` with a non-empty value, emit an event: `idkey:{pubkeyHex}`
3. Update `OwnerResult` to include the identity key:
   ```go
   type OwnerResult struct {
       Outpoint    *transaction.Outpoint `json:"outpoint"`
       Address     string                `json:"address,omitempty"`
       IdentityKey string                `json:"identityKey,omitempty"` // from MAP opns.idKey
   }
   ```
4. Update `Owner()` to return the identity key when resolving domain ownership

**Reference files:**
- `1sat-stack/pkg/opns/lookup.go` — current indexer logic
- `go-templates/template/inscription/inscription.go` — inscription parsing (MAP data follows the inscription)

### Step 4: Paymail Service Core

**New file:** `1sat-stack/pkg/paymail/service.go`

Core service holding:
- Reference to OpNS lookup service (for name → identity key resolution)
- Reference to wallet service (for `InternalizeAction`)
- Anyone key deriver for BRC-29 payment derivation

Key method — resolve alias to identity key:
```
1. Parse alias from paymail address (alice@example.com → alice)
2. Call opns.Owner("alice") → get OwnerResult with identityKey from MAP
3. Use identity public key for BRC-29 payment derivation
```

The paymail domain is just routing — any domain with a `_bsvalias._tcp` DNS SRV record pointing to the server works. The alias part of the paymail address maps directly to the OpNS name.

No registration table needed — the MAP data on-chain IS the registration.

### Step 5: Pending Payment Reference Store

**New file:** `1sat-stack/pkg/paymail/store.go`

In-memory store with TTL for pending payment references:
```go
type PendingPayment struct {
    Reference        string    // unique random ID
    IdentityPubKey   string    // from OpNS MAP opns.idKey (for InternalizeAction)
    DerivationPrefix string    // random base64
    DerivationSuffix string    // random base64
    Satoshis         uint64
    OutputScript     string    // pre-computed P2PKH script hex
    CreatedAt        time.Time
    ExpiresAt        time.Time // TTL ~15 minutes
}
```

### Step 6: Paymail Endpoints

**New file:** `1sat-stack/pkg/paymail/routes.go`

Fiber handlers following the paymail-brc100 wire format:

1. **Capability Discovery** — `GET /.well-known/bsvalias`
2. **PKI** — `GET /v1/bsvalias/id/:paymail`
3. **P2P Payment Destination** — `POST /v1/bsvalias/p2p-payment-destination/:paymail`
4. **Receive BEEF Transaction** — `POST /v1/bsvalias/receive-beef/:paymail`
5. **Receive Raw TX** — `POST /v1/bsvalias/receive-transaction/:paymail`

See previous plan revision for detailed endpoint specifications — the wire format is unchanged. The only difference is that alias → identity key resolution now reads MAP data from the OpNS overlay instead of a registration table.

**Reference files:**
- `paymail-brc100/app/api/paymail/` — wire format reference

### Step 7: Paymail Configuration & Wiring

**File:** `1sat-stack/cmd/server/config.go`

Add paymail config and wire into server initialization. Register `/.well-known/bsvalias` at app root alongside `/.well-known/auth`.

**Domain routing:** Paymail clients resolve the server via DNS SRV record (`_bsvalias._tcp.<domain>`) or fall back to `<domain>:443`. Any domain with a SRV record pointing to the server works. The capability document URLs can use the request's `Host` header, so no hardcoded domain is needed. The alias part of the paymail address (`alice@example.com` → `alice`) maps directly to the OpNS name.

### Step 8: MessageBox Integration (parallel track)

**Repo:** `go-messagebox-server` (to be integrated into 1sat-stack)

MessageBox provides an alternative delivery mechanism:
- Sender pushes payment (BEEF + derivation info) to recipient's message box
- 1sat-stack picks it up immediately and calls `InternalizeAction`
- Uses the same identity resolution (OpNS MAP → identity key → BRC-29 derivation)

MessageBox uses BRC-31 (Authrite) authentication. The identity key from the OpNS MAP field is used for both routing messages and deriving payment addresses.

This is a parallel track — paymail and messagebox can coexist. Implementation details TBD after paymail is working.

---

## Design History

### Previous approaches considered and rejected

**P2PK locking script approach** (sessions 1-2): Put the OpNS token at a P2PK script locked to a derived public key. Rejected because:
- Required a new P2PK decoder in go-templates
- Required OpNS indexer changes to detect P2PK scripts
- Required a special key derivation scheme (and the derivation was an unresolved design question)
- BRC-100 wallet can't sign with the identity root key directly, so a derived key was needed anyway
- The unsolicited-send attack: anyone could send OpNS tokens to a publicly derivable address, creating false identity associations

**Server-side registration-only approach** (session 2): Registration endpoint stores identity↔OpNS mapping. Rejected because:
- External users can't discover the identity key without talking to the specific 1sat-stack server
- The binding only exists in one server's database, not on-chain

**BAP shared derivation** (session 2): Use BAP's `deriveChild(anyone, "1-bap-identity")` for OpNS. Rejected because:
- BAP currently uses counterparty=self, changing it is a separate decision
- Ties OpNS identity binding to BAP's signing key, which serves a different purpose
- Same unsolicited-send attack applies

### Why MAP is better

The MAP approach resolves all the above issues:
- **No special derivation** — identity key is written explicitly as data, not encoded in a script address
- **No P2PK** — standard P2PKH ordinals, works with existing wallet infrastructure
- **No registration endpoint** — on-chain MAP data is the registration
- **Externally discoverable** — anyone can read the identity key from ORDFS
- **Opt-in** — the wallet owner explicitly writes the MAP data; unsolicited tokens don't have it
- **Revocable** — re-inscribe to update or remove the binding
- **Follows existing patterns** — MAP is already used throughout the 1sat ecosystem

---

## Verification Plan

1. **Unit test: BRC-29 payment derivation roundtrip** — derive payment address server-side (anyone + identity pubkey), derive payment private key wallet-side (identity privkey + anyone pubkey), confirm address matches

2. **Unit test: MAP script building** — verify `MAP.set()` from `@bopen-io/templates` produces correct `OP_RETURN MAP SET opns.idKey <pubkey>` output

3. **Integration test: OpNS register action** — create OpNS ordinal, register with MAP `opns.idKey`, verify overlay indexes the identity key, verify `Owner()` returns the identity key

4. **Integration test: Paymail endpoints** — register OpNS name with identity key, call capability discovery, request payment destination, build test transaction to the returned address, submit via receive-beef, confirm wallet internalized the payment

5. **Integration test: Deactivation** — re-inscribe with empty MAP `opns.idKey`, verify overlay removes the identity binding, verify paymail resolution fails

---

## Open Items (for future iterations)

- **MessageBox integration** — parallel delivery mechanism alongside paymail (Step 8)
- **Multi-output payments** — current design returns a single output per destination request
- **Paymail domain policy** — which domains the server should accept (allowlist vs. accept any domain that DNS-routes to us)
- **User management layer** — profile info, preferences, access control (separate from identity binding)
- **Trust model** — should the server trust any MAP `opns.idKey`, or validate that the identity key actually corresponds to a known wallet?
