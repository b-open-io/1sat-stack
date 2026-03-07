# Persistent Storage for Paymail Pending Payments and Auth Sessions

Status: **In Progress**

## Context

Two critical in-memory stores cause data loss on server restart:

1. **Paymail pending payments** (`pkg/paymail/store.go`) — in-memory map with 15-min TTL. If the server restarts while a payment is pending, the random derivation prefix/suffix is lost permanently. Two payments have already been lost this way.

2. **Auth sessions** (`cmd/server/config.go` L746) — go-sdk's `DefaultSessionManager` uses `sync.Map` in memory. All BRC-103/104 sessions lost on restart.

### Approach

- **Paymail**: SQLite database at `~/.1sat/paymail.db` — relational data with a clear schema, queryable by alias/txid/reference
- **Sessions**: Standalone Badger instance at `~/.1sat/sessions/` — pure key-value with native TTL for auto-expiry

Both fronted by simple interfaces so providers can be swapped later.

### Derivation strategy: keep random

Persistence is the correct fix. Deterministic derivation introduces address reuse.

### Lost payments

The two already-lost payments are unrecoverable.

---

## Part 1: Persist Paymail Pending Payments (SQLite)

### 1.1 Expand `PendingPayment` struct

Add alias, domain, txid to capture full request context:

```go
type PendingPayment struct {
    Reference        string    `json:"reference"`
    Alias            string    `json:"alias"`
    Domain           string    `json:"domain"`
    IdentityPubKey   string    `json:"identityPubKey"`
    DerivationPrefix string    `json:"derivationPrefix"`
    DerivationSuffix string    `json:"derivationSuffix"`
    Satoshis         uint64    `json:"satoshis"`
    OutputScript     string    `json:"outputScript"`
    TxID             string    `json:"txid,omitempty"`
    CreatedAt        time.Time `json:"createdAt"`
    ExpiresAt        time.Time `json:"expiresAt"`
}
```

### 1.2 Define `PendingStore` interface

```go
type PendingStore interface {
    Create(ctx context.Context, p *PendingPayment) error
    Get(ctx context.Context, reference string) (*PendingPayment, error)
    Update(ctx context.Context, p *PendingPayment) error
    Delete(ctx context.Context, reference string) error
    Close() error
}
```

### 1.3 SQLite implementation — `pkg/paymail/store_sqlite.go`

- Uses `mattn/go-sqlite3` (already in dependency tree)
- Database at `~/.1sat/paymail.db` (configurable)
- Schema: single `pending_payments` table matching struct fields
- Auto-creates table on open
- Cleanup goroutine deletes expired rows periodically

### 1.4 Update service, config, routes

- `NewService` accepts `PendingStore` interface
- `DerivePaymentDestination` gains `alias, domain` params
- Routes set `TxID` on delivery via `store.Update`
- Config creates SQLite store, passes to service

---

## Part 2: Persist Auth Sessions (Badger with TTL)

### 2.1 Badger implementation — `pkg/auth/session_badger.go`

- Standalone Badger at `~/.1sat/sessions/`
- Implements go-sdk `auth.SessionManager` interface
- Native TTL for auto-expiry (default 24h)
- Key layout: `n:{nonce}` for sessions, `id:{keyHex}:{nonce}` for identity index

### 2.2 Wire in config

- Replace `sdkauth.NewSessionManager()` with `auth.NewBadgerSessionManager(...)`
- Add `auth.session_path` and `auth.session_ttl` config

---

## File Change Summary

| File | Change |
|------|--------|
| `pkg/paymail/store.go` | Expand struct, define `PendingStore` interface |
| `pkg/paymail/store_sqlite.go` | **New**: SQLite-backed `PendingStore` |
| `pkg/paymail/service.go` | Accept `PendingStore`; add alias/domain to derivation |
| `pkg/paymail/config.go` | Add SQLite path config, create store in `Initialize` |
| `pkg/paymail/routes.go` | Pass alias/domain; set TxID on delivery; pass context |
| `pkg/auth/session_badger.go` | **New**: `BadgerSessionManager` |
| `pkg/auth/config.go` | Add session path and TTL config |
| `cmd/server/config.go` | Create stores; wire into services |

## Sequencing

1. Paymail store first (higher risk — payments are being lost)
2. Session manager second
3. Deploy to rack, restart, verify both survive

## Verification

1. `go build ./cmd/server` — no compile errors
2. Deploy to rack, send a test paymail payment
3. Verify record contains alias, domain, identityKey, derivation info
4. Verify TxID written back on delivery
5. Restart server, verify pending payment survives
6. Verify session survives restart
