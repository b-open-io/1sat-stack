# Admin UI: OpNS Name Publishing + Registration

## Context

The admin UI is a React + TypeScript SPA (Vite build, Go `embed.FS` at `/1sat/admin/`) with BRC-103/104 wallet authentication via Yours Wallet (`window.CWI`). It has a first-run SetupWizard, API key fallback for dev/agent access, and sections for managing overlays, tokens, and sync progress.

The 1sat-sdk has `opnsRegister`/`opnsDeregister` actions that publish OpNS names by binding an identity key via MAP metadata (`opns.idKey`). Once published, the server's paymail plugin can receive payments for that name.

## Goals

1. **Publish OpNS names** from the admin UI using the SDK's `opnsRegister` action via CWI
2. **Trigger OpNS genesis crawl** on demand (one-time sync, not config-driven)
3. **Eventually: OpNS mining** as a paid service (server-side PoW)

## Current State

### Implemented
- React admin UI with 8 sections (Whitelist, Blacklist, Workers, Topics, Lookups, ZSetLookup, Progress, OpNS)
- BRC-103/104 auth via `pkg/auth/` with go-bsv-middleware + Yours Wallet
- API key fallback (`X-Api-Key` header) for dev/agent access
- SetupWizard for first admin identity enrollment
- OpNS section skeleton with:
  - Crawl trigger button (`POST /admin/api/opns/crawl`)
  - Discover My Names button (placeholder — calls CWI `listOutputs`)
  - Publish button per name (placeholder — not yet wired to SDK action)
- SDK dependencies installed (`@1sat/actions`, `@1sat/types`, `@bsv/sdk`)

### Open Questions (to work through iteratively)

- **Name discovery**: How to find which OpNS names the connected wallet owns. The overlay doesn't track current ownership. Options: wallet `listOutputs`, 1sat indexer owner endpoint, or a combination.
- **BEEF sourcing**: The `opnsRegister` action needs `inputBEEF` for the ordinal. Does this come from `listOutputs` with `include: 'entire transactions'`, or from the server's BEEF storage?
- **Action context**: The SDK action needs `OneSatContext` with `.wallet` (CWI) and `.services` (overlay client). Need to build a bridge from CWI to this context.
- **Mining**: Server-side PoW registration as a paid service. Requires OpNS contract unlocking via go-templates, wallet funding, and overlay submission. Deferred until publish flow works.

## Architecture

### Auth
- **Wallet auth**: BRC-103/104 mutual authentication via go-bsv-middleware. Yours Wallet extension provides identity. Session-based (shared SessionManager).
- **API key**: `X-Api-Key` header bypasses wallet auth. For dev/agent access. Configured via `auth.api_key` or `ONESAT_AUTH_API_KEY` env var.
- **Admin guard**: Checks identity against `s:admin:pubkeys` store set. API key auth bypasses the guard.

### Admin UI
- React 19 + TypeScript + Vite, built to `admin/ui/dist/`, embedded in Go binary
- `window.CWI` for wallet interaction (extension-only for now)
- `AuthFetch` from `@bsv/sdk` for signed API requests
- Section components follow a consistent pattern: `showToast` prop, `apiFetch` for API calls

### OpNS Crawl
- `pkg/opns/crawl.go` — `GenesisCrawl` walks mine tree from genesis via JungleBus
- Triggered via `POST /admin/api/opns/crawl` (admin guard required)
- Creates crawl on demand if not already running, starts in background goroutine

## Key Files

- `admin/ui/src/sections/OpNS.tsx` — OpNS admin section component
- `admin/ui/src/api.ts` — API client with AuthFetch
- `admin/ui/src/App.tsx` — Main app, wallet connection, section grid
- `admin/routes.go` — Admin API routes + crawl trigger handler
- `admin/config.go` — Admin deps including `OpnsCrawlFunc` callback
- `pkg/auth/middleware.go` — BRC-103/104 middleware with API key bypass
- `pkg/auth/admin.go` — AdminGuard middleware
- `pkg/opns/crawl.go` — Genesis crawl worker
- `cmd/server/config.go` — Service wiring
- `1sat-sdk/packages/actions/src/opns/index.ts` — SDK `opnsRegister` action
