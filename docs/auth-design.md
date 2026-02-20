# Authentication & Usage Tracking Design

## Overview

Add BRC-103/104 mutual authentication to 1sat-stack for identity tracking, usage metering, and admin authorization. Initial rollout allows unauthenticated requests (tracking identity when present) without breaking existing clients.

## Architecture

```
                     ┌──────────────────────────┐
                     │  Reverse Proxy (nginx/CF) │
                     │  - IP-based rate limiting  │
                     │  - Anonymous throttling    │
                     └────────────┬───────────────┘
                                  │
                     ┌────────────▼───────────────┐
                     │        1sat-stack           │
                     │                             │
                     │  ┌───────────────────────┐  │
                     │  │ BRC-103/104 Auth MW   │  │
                     │  │ (AllowUnauthed=true)  │  │
                     │  │ via Fiber adapter     │  │
                     │  └───────────┬───────────┘  │
                     │              │               │
                     │  ┌───────────▼───────────┐  │
                     │  │ Usage Tracking MW     │  │
                     │  │ identity -> counters  │  │
                     │  └───────────┬───────────┘  │
                     │              │               │
                     │  ┌───────────▼───────────┐  │
                     │  │ Route Handlers        │  │
                     │  │ beef,txo,bsv21,...    │  │
                     │  └───────────────────────┘  │
                     │                             │
                     │  ┌───────────────────────┐  │
                     │  │ Admin Routes          │  │
                     │  │ pubkey allowlist +    │  │
                     │  │ bearer token fallback │  │
                     │  └───────────────────────┘  │
                     └─────────────────────────────┘
```

## Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Fiber vs net/http mismatch | Fiber adapter in go-bsv-middleware (upstream PR) | Keeps BRC-103/104 logic in the shared library; 1sat-stack stays on Fiber |
| Initial auth posture | `AllowUnauthenticated=true` on all public routes | Gather identity/usage data without breaking existing clients |
| Client signing (SDK) | `@bsv/sdk` `AuthFetch` in 1sat-sdk's `BaseClient` | AuthFetch already implements BRC-104 with session management, nonces, 402 handling |
| Admin UI wallet integration | `window.CWI` (injected by yours-wallet extension) | `CWI` directly implements `WalletInterface`; pass to `AuthFetch` with no adapter needed |
| Admin authorization | Public key allowlist in config + bearer token fallback | Pubkey allowlist for wallet-based auth; bearer token kept for automation/CI |
| Server identity key | Reuse `server_private_key` from wallet config | Single server identity; avoids config proliferation |
| Anonymous rate limiting | External only (reverse proxy / CDN) | Clean separation of concerns; 1sat-stack only tracks identity-based usage |
| Usage tracking storage | In-memory counters + periodic flush to BadgerDB | Low overhead; queryable via admin API |
| Payment middleware | Designed for, not implemented | Architecture supports per-route `PaymentMiddlewareFactory` later |

## How BRC-103/104 Auth Works

1. Client sends `POST /.well-known/auth` with an `initialRequest` message containing its public key and a nonce
2. Server responds with its own public key, nonce, and signature proving it holds the corresponding private key
3. Client verifies, then subsequent requests include `x-bsv-auth-*` headers: identity key, nonce, signature over the request payload
4. Server verifies the signature, extracts the identity, and stores it in the request context
5. Nonce chain prevents replay attacks; sessions are managed per identity key

## Implementation Plan

### Phase 1: go-bsv-middleware -- Fiber Adapter

Submit a PR to `bsv-blockchain/go-bsv-middleware` adding Fiber support.

| # | Task |
|---|------|
| 1.1 | Add `pkg/middleware/fiber/` package -- adapter that wraps `AuthMiddlewareFactory.HTTPHandler` for Fiber's middleware chain. Converts `*fiber.Ctx` to/from `http.Request`/`http.ResponseWriter`. |
| 1.2 | Fiber identity helpers -- `ShouldGetIdentity(c *fiber.Ctx)`, `IsNotAuthenticated(c *fiber.Ctx)` wrapping the existing context helpers via `c.UserContext()`. |
| 1.3 | Fiber payment adapter (stub) -- same pattern for `PaymentMiddlewareFactory`. Not required yet but maintains parity. |
| 1.4 | Integration tests using Fiber test server + `AuthFetch` client from go-sdk. |
| 1.5 | Submit PR upstream. |

### Phase 2: 1sat-stack -- Auth Middleware + Usage Tracking

| # | Task |
|---|------|
| 2.1 | Add `go-bsv-middleware` dependency to `go.mod`. |
| 2.2 | Server wallet setup -- create a `wallet.Interface` from the existing `server_private_key` config. |
| 2.3 | Apply auth middleware globally in `RegisterRoutes` on the API group with `AllowUnauthenticated=true`. The `/.well-known/auth` endpoint is handled by the middleware automatically. |
| 2.4 | Usage tracking middleware -- runs after auth. Reads identity from context, increments atomic in-memory counters (request count, bytes, per-route-group). Background goroutine flushes to BadgerDB on a configurable interval. |
| 2.5 | Usage tracking admin endpoints -- `GET /admin/usage` (all identities), `GET /admin/usage/:identityKey` (single identity). |
| 2.6 | Admin pubkey allowlist -- config field `admin.allowed_keys`. Middleware checks `ShouldGetAuthenticatedIdentity()` against the list. Bearer token kept as fallback. |
| 2.7 | Configuration additions to `config.yaml` schema. |

#### Configuration Shape

```yaml
auth:
  enabled: true
  server_private_key: "..."   # reuses wallet.server_private_key if not set
  allow_unauthenticated: true  # phase 1 default

admin:
  routes:
    allowed_keys:
      - "02abc..."
      - "03def..."
    bearer_token: "..."  # backward compat

usage:
  enabled: true
  flush_interval: 60s
  store_prefix: "usage:"
```

### Phase 3: Admin UI -- CWI Integration

| # | Task |
|---|------|
| 3.1 | Bundle `@bsv/sdk`'s `AuthFetch` into the admin UI (minimal JS build). |
| 3.2 | Wallet connection flow -- detect `window.CWI`, prompt to install yours-wallet if absent. Create `AuthFetch(window.CWI)` for all admin API calls. |
| 3.3 | Usage dashboard -- add a usage metrics view calling the new admin endpoints. |

### Phase 4: 1sat-sdk -- Client Auth

| # | Task |
|---|------|
| 4.1 | Extend `ClientOptions` with optional `wallet?: WalletInterface` field. |
| 4.2 | In `BaseClient`, when `wallet` is provided, create `AuthFetch` internally and use it instead of plain `fetch`. |
| 4.3 | Backward compatible -- no wallet = plain fetch, no auth headers. |
| 4.4 | Tests against a mock server with auth middleware. |

## Projects Touched

| Project | Repo | Changes |
|---------|------|---------|
| go-bsv-middleware | `bsv-blockchain/go-bsv-middleware` | New `pkg/middleware/fiber/` adapter package |
| 1sat-stack | `b-open-io/1sat-stack` | Auth middleware, usage tracking, admin allowlist, admin UI, config |
| 1sat-sdk | `b-open-io/1sat-sdk` | `BaseClient` auth integration, `ClientOptions` extension |

## Implementation Order

1. **go-bsv-middleware** Fiber adapter (unblocks everything else)
2. **1sat-stack** auth middleware + usage tracking
3. **1sat-stack** admin pubkey allowlist + admin UI CWI integration
4. **1sat-sdk** `AuthFetch` in `BaseClient`

## Future Work (Designed For, Not Implemented)

- **Payment middleware**: Per-route `PaymentMiddlewareFactory` with configurable price calculators (402 flow)
- **Identity-based rate limiting**: Usage counters in BadgerDB become the foundation for per-identity limits
- **Certificate-based authorization**: BRC-103 certificate exchange for role-based access beyond the admin allowlist
