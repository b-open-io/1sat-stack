# External Arcade Migration — Execution Plan

Status: **In Progress**

## Context

Arcade has been refactored into a multi-process HTTP service and is deployed at `https://arcade.gorillapool.io` (verified live, post-refactor — responds to `/health`, `/events`, `/tx/:txid`). 1sat-stack still embeds the pre-refactor arcade as a Go library pinned at `f85be2e6b9cc`. We want to remove embedded arcade entirely, point at the external instance, and preserve the "broadcast and get back a real status" experience for SDK callers.

The architectural decision: **1sat-stack hosts its own broadcast endpoint at `/1sat/tx`** that internally talks to arcade. The SDK doesn't talk to arcade directly — it talks to 1sat-stack. This keeps multi-tenant arcade traffic (other people's txs) out of 1sat-stack's view, lets 1sat-stack maintain a single SSE subscription tracking only its own broadcasts, and gives wallet/SDK callers a synchronous-feeling broadcast API even though arcade itself is async.

## Architecture

### 1sat-stack runs an always-on SSE consumer

A single long-lived SSE connection to `https://arcade.gorillapool.io/events` with a fixed callback token (stored in config DB as `arcade.callback_token`). Every transaction that 1sat-stack broadcasts to arcade — regardless of source — is submitted with this same token in `X-CallbackToken`, so it shows up on the SSE stream. Reconnection uses `Last-Event-ID` (persisted across restarts) to catch up on events missed during downtime. We never close this connection.

Inside 1sat-stack, an in-memory event broker fans out events from the SSE consumer to subscribers:

- **Per-call waiters** — `/1sat/tx` handler registers a waiter channel keyed by txid before submitting; the broker sends events for that txid to the waiter; handler unregisters after responding.
- **Always-on handlers** — overlay ingest, merkle proof attachment. Every event flows through these regardless of whether a waiter is registered.

No separate persistent "pending set" needed. The SSE stream is the source of truth; BEEF is already stored separately; ingestion is idempotent.

### `pending_auditor` stays as the safety net

For events lost to disconnection longer than arcade's SSE replay window, `pkg/indexer/pending_auditor.go` keeps polling `GET /tx/:txid` every 10 minutes for unconfirmed txs. The two existing call sites at `pending_auditor.go:279` (unconfirmed → look for proof) and `pending_auditor.go:356` (confirmed-but-proofless → refetch) both stay relevant; only the underlying arcade call swaps to the new HTTP client.

### `/1sat/tx` and `/1sat/tx/:txid`: 1sat-stack's broadcast surface

Arcade-shaped, but with the headers limited to what an external caller needs:

| Endpoint | Method | Behavior |
|---|---|---|
| `/1sat/tx` | POST | Accepts raw tx bytes (octet-stream). Computes txid, registers waiter, submits to arcade with our callback token, waits up to 30s for terminal/accepted status, returns full `TransactionStatus` JSON. On `REJECTED`/`DOUBLE_SPEND_ATTEMPTED`: 4xx with `extraInfo`. On timeout: 202 with last-known status. |
| `/1sat/tx/:txid` | GET | Direct passthrough to arcade `GET /tx/:txid`. Returns full `TransactionStatus`. |

Headers stripped from the public surface: `X-CallbackUrl`, `X-CallbackToken`, `X-FullStatusUpdates` (these are 1sat-stack-internal; external callers can't register their own callbacks). `X-SkipFeeValidation` and `X-SkipScriptValidation` are dead code on arcade `origin/main` anyway.

### Overlay engine broadcaster

`pkg/overlay/config.go ModuleDeps` does not currently have a `Broadcaster` field — overlays don't broadcast today. With the migration, each overlay module gets a `Broadcaster transaction.Broadcaster` injected, implemented as a small wrapper around `arcadeclient` that submits with our callback token in `X-CallbackToken`. So overlay-engine-initiated broadcasts also surface on our SSE stream and get tracked the same way as everything else. (Off-the-shelf go-sdk `broadcaster.Arc{ApiUrl}` doesn't support custom headers — that's why we wrap our own.)

### Paymail stays as-is

Paymail spec expects synchronous broadcast confirmation. Behavior unchanged — same 4xx-on-rejected, 200-on-accepted contract. The only change is the underlying broadcast call swaps from the embedded `Arcade().SubmitTransaction` to invoking the same internal `SubmitAndWait` that `/1sat/tx` uses. Single broadcast chokepoint.

### SDK changes are narrow

`@1sat/client` `ArcadeClient` is **untouched**. It remains an arcade-direct client (anyone wanting to talk to arcade.gorillapool.io directly can use it). Whether it happens to work against 1sat-stack on compatible calls is incidental and not the design intent.

The actual SDK changes:

- `OneSatServices.postBeef` is rewritten to POST directly to 1sat-stack's `/1sat/tx` using the existing `BaseClient.request` infrastructure (no `ArcadeClient` involvement).
- `OneSatServices.getStatusForTxids` is rewritten similarly to GET `/1sat/tx/:txid`.
- The existing `arcade` field on OneSatServices is removed if nothing external uses it.
- `getPolicy` is dropped from any caller paths (verified — the route doesn't exist on arcade `origin/main bc96604`; only the model struct survives, internal-use only).
- Method signatures and return shapes are unchanged → consuming apps (yours-wallet, 1sat-website, sigma-auth, bsv-desktop, sweep-ui) need no source changes, just a `@1sat/client` bump.
- HTTP client must allow ~35s timeout (server holds the connection during the 30s wait + arcade roundtrip).

No SSE consumer in TypeScript. No callback tokens in TypeScript. No per-tx waiter logic in TypeScript. The server does all of it.

## Critical files

### 1sat-stack (Go)

**New packages**:
- `pkg/arcadeclient/` — HTTP client (`Submit`, `GetStatus`, `Subscribe`, `SubmitAndWait`)
- `pkg/eventbroker/` (or merged into arcadeclient) — in-memory fan-out from SSE consumer
- New broadcast route handlers — `/1sat/tx` and `/1sat/tx/:txid` (location TBD; either inside `cmd/server/` or a new `pkg/broadcast/`)

**Modified**:
- `cmd/server/arcade_wrapper.go` — rewrite `BeefCapturingArcadeService` to wrap the new arcadeclient
- `cmd/server/config.go` — drop embedded arcade init (lines ~325-335, 482-486, 893-963), drop `/1sat/arcade` route mount (line 1678-1679), mount new `/1sat/tx` routes, construct event broker, start SSE consumer goroutine
- `pkg/paymail/{config.go,service.go,routes.go}` — switch broadcast call sites at `routes.go:241` and `routes.go:344` to use the same internal `SubmitAndWait` that `/1sat/tx` invokes
- `pkg/indexer/pending_auditor.go` — swap `arcadeService.GetStatus` calls to new arcadeclient
- `pkg/overlay/config.go` `ModuleDeps` — add `Broadcaster` field; inject the custom wrapper into all 5 modules' `NewModuleEngine` calls
- `pkg/config/apply.go` — add `arcade.url`, `arcade.callback_token`, `arcade.timeout` keys to `RuntimeConfig`
- `admin/ui/src/pages/SettingsPage.tsx` — rework Arcade panel: replace embedded mode/path/teranode-URLs/log-level fields with URL/callback-token/timeout inputs
- `go.mod` — drop `replace github.com/bsv-blockchain/arcade => ...`; pin to current `origin/main` HEAD `bc96604` (or a tag if one ships); drop imports of `arcade/service`, `arcade/routes/fiber`, `arcade/config`, `arcade/events`; keep `arcade/models` for status enums and `TransactionStatus` shape

**Deleted**:
- `pkg/indexer/arcade_listener.go` — in-process channel listener; SSE replaces it, but the always-on event handler in 1sat-stack's broker subsumes its responsibilities
- `handleArcCallback` in `pkg/merkle/routes.go:30` — inbound webhook from arcade; SSE replaces it
- The `/1sat/arc` route mount at `cmd/server/config.go:1685`

### SDK (TypeScript)

**Modified**:
- `1sat-sdk/packages/client/src/services/OneSatServices.ts` — rewrite `postBeef` and `getStatusForTxids`; remove `arcade` field if unused externally; drop any `getPolicy` code paths
- Search across `1sat-sdk/packages/*/src` for any remaining `getPolicy` callers and remove them

**Untouched**:
- `1sat-sdk/packages/client/src/services/ArcadeClient.ts` — stays as-is, remains an arcade-direct client
- `1sat-sdk/packages/types/src/services.ts` — service union literal stays

### Consuming apps

No source changes required. Bump `@1sat/client` once the SDK ships.

## Verification

End-to-end on ovh-n0001 after deploy:

1. Set `arcade.url=https://arcade.gorillapool.io` and a generated `arcade.callback_token` in config DB via admin UI.
2. Restart 1sat-stack. Confirm SSE connection establishes (check `~/.1sat/logs.db` for SSE lifecycle events).
3. Run `1sat tokens deploy-mint` from CLI against ovh-n0001's stack — expect a `TransactionStatus` with `txStatus = ACCEPTED_BY_NETWORK` (or better) returned within 30s.
4. Send a paymail tx to a 1sat-stack-hosted address — confirm sender gets 200 with txid for accepted, 4xx with `extraInfo` for malformed.
5. Submit deliberately-malformed BEEF via `/1sat/tx` — confirm 4xx response with `txStatus = REJECTED`.
6. Verify pending_auditor's 10-minute polling cycle still attaches merkle proofs for unconfirmed txs.
7. Restart 1sat-stack while a tx is in-flight at arcade — confirm `Last-Event-ID` checkpoint persists, SSE replays missed events on reconnect, ingestion completes.
8. Confirm overlay-engine-initiated broadcasts (e.g. via BSV21 cleanup flows that produce on-chain output) flow through the broadcaster wrapper and surface on the SSE stream.

## Out of scope (deferred)

- SSE replay-window edge cases when 1sat-stack down longer than arcade event retention — pending_auditor catches it eventually; harden later if needed.
- Multi-instance 1sat-stack deployment — single-instance on ovh-n0001 today; revisit if we ever scale horizontally.
- Migration-day backlog of pending txs that were tracked by the old embedded arcade — they'll be discovered via pending_auditor polling rather than SSE.
- Paymail message-box-only refactor — discussed and tabled; paymail spec needs sync ack, so paymail stays as-is.
- Overlay broadcaster's wait-vs-fire-and-forget semantics — default to fire-and-forget; the engine reacts to status updates via SSE. Revisit if a specific overlay needs sync wait.
- 1sat-sdk Destination + mintBsv21 refactor — independent track.

## Suggested execution order

1. `pkg/arcadeclient` (Go) — Submit, GetStatus, Subscribe primitives.
2. Event broker + always-on SSE consumer.
3. `/1sat/tx` and `/1sat/tx/:txid` handlers using `SubmitAndWait`.
4. Rewrite `BeefCapturingArcadeService` against new arcadeclient.
5. Migrate paymail to internal `SubmitAndWait`.
6. Migrate `pending_auditor`.
7. Wire overlay engine `Broadcaster` (custom wrapper).
8. Delete `arcade_listener.go` and `handleArcCallback`.
9. Update admin UI Arcade panel.
10. Drop `/1sat/arcade` and `/1sat/arc` route mounts in `cmd/server/config.go`.
11. Drop arcade `replace` from `go.mod`; verify build clean (compiler surfaces any forgotten imports).
12. Rewrite `OneSatServices.postBeef` and `getStatusForTxids` (SDK).
13. Drop `getPolicy` references across SDK.
14. Bump `@1sat/client` and downstream packages; consuming apps pick up via `bun install`.
15. Deploy to ovh-n0001, run verification.

Estimated effort: ~5 working days end-to-end. Steps 1-3 (arcadeclient + event broker + new routes) are the bulk of the new code; everything after is mostly call-site swaps and deletions.
