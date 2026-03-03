# OpNS Overlay Simplification + ORDFS Integration

## Context

The OpNS overlay currently tracks domain ownership with events for addresses, identity keys, origins, ordlock listings, and mining status. This is redundant now that ORDFS can resolve any ordinal to its current state (including merged MAP data) given just the origin outpoint.

Additionally, InternalizeAction (used by paymail to receive payments) does NOT broadcast transactions. The paymail service needs to broadcast through Arcade synchronously so the sender gets immediate confirmation or error.

## Design

### 1. OpNS Overlay — Strip to Name Registry

The overlay becomes a thin name-to-origin mapping. It tracks two things:

- `opns:{domain}` → the origin outpoint of the ordinal holding this name
- `mine:{prefix}` → mining hierarchy for domain availability

Everything else is removed from `OutputAdmittedByTopic()`:
- `origin:` events — ORDFS handles origin resolution
- `p2pkh:` events — not the overlay's concern
- `idkey:` events — ORDFS returns merged MAP data
- `list:` events — ordlock status not needed

`Owner()`, `OwnerResult`, and `GET /opns/owner/{name}` are deleted. The paymail PKI endpoint replaces the public-facing name lookup. `Mine()` and `GET /opns/mine/{name}` stay.

New method replaces Owner:

```go
// Origin returns the origin outpoint for a registered OpNS domain.
func (l *LookupService) Origin(ctx context.Context, domain string) (*transaction.Outpoint, error)
```

This queries the `opns:{domain}` ZSet to find any output, loads that output's events, and returns the origin. The origin never changes for a given ordinal, so this is stable.

### 2. Paymail Service — ORDFS + Arcade Integration

The paymail `Service` struct gets two new dependencies:

```go
type Service struct {
    opns          *opns.LookupService
    ordfs         *ordfs.Ordfs              // NEW
    arcade        arcadeservice.ArcadeService // NEW
    wallet        wallet.Interface
    store         *Store
    anyoneDeriver *wallet.KeyDeriver
    logger        *slog.Logger
}
```

**ResolveIdentityKey** changes from querying OpNS directly to a two-step resolution:

```
1. opns.Origin("alice") → origin outpoint
2. ordfs.Load(ctx, {Outpoint: origin, Seq: -1, Map: true}) → Response
3. json.Unmarshal(resp.Map) → extract "opns.idKey"
4. If empty/missing → error (paymail returns 404)
5. If present → parse as *ec.PublicKey, return
```

### 3. Paymail Receive — Synchronous Broadcast via Arcade

InternalizeAction does NOT broadcast transactions. It stores them with status `TxStatusUnproven` — the monitor daemon won't pick these up either (it only processes `Unsent`/`Sending`).

The paymail receive flow must broadcast explicitly:

```
1. Receive BEEF from sender
2. Verify payment output matches pending destination
3. Broadcast through Arcade synchronously
   - arcade.SubmitTransaction(ctx, txBytes, nil)
   - If Arcade rejects → return error to sender
4. InternalizeAction → store payment in wallet
5. Return success with txid
```

This gives the sender immediate feedback. If broadcast fails (double-spend, invalid TX), they know before the HTTP response.

### 4. Server Wiring (cmd/server/config.go)

Paymail initialization gets ORDFS and Arcade:

```go
// After ORDFS, Arcade, and wallet initialization
paymailSvc := paymail.NewService(
    svc.OPNS.Lookup,
    svc.ORDFS.Ordfs,        // NEW
    svc.Arcade.ArcadeService, // NEW
    serverWallet,
    logger,
)
```

### What doesn't change

- Paymail wire format (capabilities, PKI, destinations, receive-beef, receive-transaction)
- BRC-29 derivation (same protocol, same anyone key)
- Pending payment store
- InternalizeAction call (still happens, just after broadcast)
- TopicManager (topic.go) — admission logic stays
- `Mine()` method and route

### Data flow summary

```
Paymail request for alice@example.com
  → parsePaymail → "alice"
  → opns.Origin("alice") → origin outpoint
  → ordfs.Load(origin, seq=-1, map=true) → merged MAP JSON
  → extract "opns.idKey" → identity public key
  → BRC-29 derivation → payment destination

Payment received:
  → verify output matches pending payment
  → arcade.SubmitTransaction(beefBytes) → synchronous broadcast
  → wallet.InternalizeAction(beef, outputs, description) → store in wallet
  → 200 OK to sender
```

## Relationship to other plans

This supersedes Step 3 of the paymail-opns-bap design (`2026-02-25-paymail-opns-bap-design.md`). That plan had the overlay tracking identity keys via MAP events — we're removing that in favor of ORDFS resolution.

The MAP templates migration (`2026-03-02-map-templates-migration.md`) is being executed in parallel. Task 6 of that plan (updating the Go indexer MAP check) should be skipped — the MAP parsing block we added to lookup.go is being removed entirely by this plan.
