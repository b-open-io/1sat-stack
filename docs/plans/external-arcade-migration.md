# Migrate 1sat-stack to External Arcade + Finish Broadcast Pipeline

## Context

The original plan (arcade response status fix + 1sat-stack Broadcaster wiring + push 1sat-sdk work) has been overtaken by an arcade refactor we hadn't yet picked up. Arcade `origin/main` (HEAD `45d0614`) is fundamentally restructured by `d0a3a39` "Refactor to support merkle-service integration" (~38k LOC):

- Old: in-process Go library; sync `service.ArcadeService.SubmitTransaction(ctx, rawTx, opts) → *TransactionStatus`; Fiber routes mounted under `/arcade/...` by 1sat-stack.
- New: multi-process Kafka-backed microservice; `POST /tx` returns `202 {"status":"submitted"}`; status via `GET /tx/:txid` or `GET /events` SSE or webhook callback; Gin routes root-mounted; built-in retry via `RetryCount`/`NextRetryAt`. The whole `arcade/service`, `arcade/routes/fiber`, `arcade/service/embedded` packages are deleted.

**Implications for the original plan:**

- Item #1 (arcade route handler StatusCode/Title fix) is structurally obsolete on new main. `StatusCode` is no longer set on broadcast outcomes anywhere; `txStatus` is the single source of truth. Bug class is gone for free.
- `models.StatusServiceError` was removed; replaced semantically by `StatusPendingRetry` (recoverable, arcade retries internally). The wallet-side classification problem largely resolves itself — `PENDING_RETRY` should be treated as in-flight, not invalid.
- 1sat-stack pin (`f85be2e6b9cc`, pre-refactor) cannot be advanced to current arcade main without a non-trivial rewrite of every call site.

**New scope:** migrate 1sat-stack from embedded-sync arcade to external-HTTP arcade. Keep BEEF-capture pattern. Handle the sync→async mismatch for paymail. Switch indexer event listener from in-process Go channel to SSE.

## 1sat-stack call sites that must change

(All paths under `/Users/davidcase/Source/1sat/1sat-stack/`)

| File | What it does today | Required change |
|---|---|---|
| `cmd/server/arcade_wrapper.go` | `BeefCapturingArcadeService` decorator implementing `service.ArcadeService` interface; intercepts `SubmitTransaction`/`SubmitTransactions` to ensure ancestor BEEF is populated and persisted before forward | Re-implement against a 1sat-stack-internal arcade-client interface that talks HTTP |
| `cmd/server/config.go` (~L43-45, L281-335, L482-486, L866-933) | Wires `arcadeconfig.Config.Initialize(...)` → `arcadeconfig.Services` → `BeefCapturingArcadeService`; mounts `arcaderoutes.NewRoutes{Service, Store, EventPublisher, Arcade}` under `/arcade/*` | Drop `arcade/config`, `arcade/routes/fiber`, `arcade/service`. Replace with HTTP client construction + reverse-proxy mount (or accept URL break). |
| `pkg/paymail/{config.go,service.go,routes.go}` | Holds `arcadeservice.ArcadeService`; paymail routes call `SubmitTransaction(...)` synchronously and branch on `models.StatusRejected` to return 4xx to sender, `ExtraInfo` for the body | Migrate to submit-then-poll pattern with bounded timeout |
| `pkg/indexer/arcade_listener.go` | Subscribes to in-process `events.Publisher` Go channel; reads `TxID/Status/MerklePath/ExtraInfo` and republishes to local `arc` pubsub | Replace with SSE consumer + per-event follow-up `GET /tx/:txid` to retrieve full status |
| `pkg/indexer/pending_auditor.go` | Per pending tx, calls `arcadeService.GetStatus(ctx, txid)` and reads `MerklePath` to re-attach BUMP locally | Just swap to HTTP client `GetStatus`; behavior preserved |
| `pkg/arcade/swagger.go` | References `models` for swagger doc only (no runtime) | Drop/replace if old docs no longer applicable |

## Plan items

### A. Stand up new arcade as external process

- Build `cmd/arcade --mode=all` in standalone config (`kafka.backend=memory`, `store.backend=pebble`, no merkle-service, no Aerospike)
- Smoke test locally: `POST /tx` → 202; `GET /tx/:txid` → 404 then ACCEPTED; `GET /events` SSE; submit malformed BEEF → see PENDING_RETRY/REJECTED progression in store
- On ovh-n0001: install as systemd user service alongside `1sat-stack.service`. Decide port (reachable from 1sat-stack on loopback). Decide data dir (separate from 1sat-stack's `~/.1sat/`). Decide chaintracks: arcade has its own embedded chaintracks server — coordinate to avoid running two
- Deploy: install service, verify, monitor for stability before flipping 1sat-stack to it

### B. Build 1sat-stack-internal arcade HTTP client (SSE-based)

No current Go REST client exists. (The old `arcade/client/` package was deleted in `d0a3a39`; reusable as reference for SSE parsing only.) Build fresh in 1sat-stack — likely `pkg/arcadeclient`.

Surface:

- `Submit(ctx, rawTx, opts) → (txid string, err error)` — POSTs `/tx`, expects 202, returns the parsed/computed txid
- `GetStatus(ctx, txid) → *models.TransactionStatus` — GET `/tx/:txid`, 404 → `(nil, nil)` semantics
- `Subscribe(ctx, callbackToken, lastEventID) → <-chan *SSEEvent` — long-lived SSE consumer with reconnect, parses `id:`/`event:`/`data:` framing, exposes the slim `{txid, txStatus, timestamp}` event payload
- `SubmitAndWait(ctx, rawTx, opts, timeout) → *models.TransactionStatus, error` — **the primary "sync-feel" entry point**:
  1. Pre-arm an SSE subscription bound to the client's persistent `callbackToken`
  2. `Submit(...)` with `X-CallbackToken` set to that same token, capture txid
  3. Watch the SSE channel for an event with that txid in a terminal state (`MINED`/`IMMUTABLE`/`ACCEPTED_BY_NETWORK`/`SEEN_ON_NETWORK`/`SEEN_MULTIPLE_NODES`/`REJECTED`/`DOUBLE_SPEND_ATTEMPTED`)
  4. On terminal event, follow up with `GetStatus(txid)` to fill full `MerklePath`/`ExtraInfo` and return
  5. On `ctx.Done()` or timeout (default ~30s, configurable), return the last-known status (one final `GetStatus` poll) and a sentinel error so the caller can decide whether to treat as 202 or 5xx

Other notes:

- Single shared SSE subscription per client instance (not per `SubmitAndWait` call) — SSE manager fans out events to per-txid waiters via internal channel registry. Reconnect with `Last-Event-ID` so events between reconnect/replay aren't lost.
- Define a 1sat-stack-internal interface `ArcadeClient` matching the surface above, so `BeefCapturingArcadeService` can decorate it.
- Continue importing `arcade/models` (still exists, mostly compatible — handle `Title` removal and new statuses `SEEN_MULTIPLE_NODES`/`PENDING_RETRY`/`STUMP_PROCESSING`).
- `PENDING_RETRY` is **not terminal** — `SubmitAndWait` should keep waiting through it (arcade is retrying internally).

### C. Migrate paymail to `SubmitAndWait`

- Replace sync `SubmitTransaction` call sites in `pkg/paymail/routes.go:241,344` with `client.SubmitAndWait` (default ~30s timeout, paymail can override)
- Preserve `StatusRejected` → 4xx + `ExtraInfo` body behavior
- On timeout: return 202 to paymail sender (cleanest match for paymail spec, since the tx is in-flight; old behavior was "block until verdict" but the 30s upper bound makes that tolerable)

### D. Migrate indexer arcade_listener to SSE

- Reuse the same `client.Subscribe(ctx, callbackToken, lastEventID)` SSE consumer from Item B
- Persist last-seen `Last-Event-ID` (likely in Badger, alongside other indexer state) so replay-on-restart works
- For each SSE event, follow up with `client.GetStatus(txid)` to retrieve full `TransactionStatus` (fills `MerklePath`/`ExtraInfo` omitted from slim event payload) before republishing to local `arc` pubsub
- Remove the in-process `events.Publisher` dependency from `pkg/indexer/config.go` (`ArcadeListenerDeps`)

### E. Decide `/arcade/...` URL preservation

Two options:

- **(i) Reverse-proxy**: in `cmd/server/config.go`, mount a Fiber proxy under `/arcade/*` → `http://localhost:<arcade_port>/*`. Preserves backward-compatible URLs for clients (1sat-sdk `OneSatServices`, others).
- **(ii) Break and update clients**: drop the prefix; update `OneSatServices`/`@1sat/client` and any other consumers to point directly at the new arcade URL.

Recommendation: (i) for now (smaller blast radius); revisit later. Decision needed.

### F. Update 1sat-stack go.mod

- Drop `replace github.com/bsv-blockchain/arcade => …` (or update to current main commit)
- Pin to a tagged release on arcade main (e.g. `v0.5.2`) once smoke-tested
- Keep imports of `github.com/bsv-blockchain/arcade/models` only; drop `arcade/service`, `arcade/routes/fiber`, `arcade/config`, `arcade/events`, `arcade/client`
- Verify build clean

### G. Wire Broadcaster into overlay engine (was Item #2)

Now trivial because arcade is HTTP:

- Add `Broadcaster transaction.Broadcaster` to `ModuleDeps` in `pkg/overlay/config.go`
- Construct go-sdk's `broadcaster.Arc{ApiUrl: <arcade_url>}` once in `cmd/server/config.go` and inject into all 5 modules' `ModuleDeps` (bap, bsocial, bsv21, opns, ordlock)
- Pass `Broadcaster: deps.Broadcaster` into `engine.Config{...}` in `NewModuleEngine`
- Verify go-sdk's `broadcaster.Arc.BroadcastCtx` handles 202 from new arcade — current behavior expects 200 with terminal status; may need a small adjustment in go-sdk to treat 202 as "in-flight, monitor will retry" rather than failure. Investigate at implementation time.

### H. Push 1sat-sdk Destination refactor + mintBsv21 (was Item #3)

Independent of the migration. 5 staged commits in `1sat-sdk` working tree:

- `Destination` type + `OrdinalRecipient` in `@1sat/types`
- `resolveDestination` helper in `@1sat/actions`
- `inscribe`, `sendBsv21`, `deployBsv21Mint`, `deployBsv21Auth` migrated to `Destination`
- New `mintBsv21` action with optional mint/auth + `endMinting` confirmation
- CLI commands updated; new `tokens mint` subcommand

Do anytime; recommended after A–G land so testing runs against the corrected pipeline.

## Verification (overall)

- `arcade` standalone smoke tests pass locally and on ovh-n0001
- 1sat-stack builds clean against current arcade main
- Deploy `1sat tokens deploy-mint` and `1sat tokens deploy-auth` end-to-end:
  - txs accepted by arcade (verified via `GET /tx/:txid`)
  - landed in overlay topic DBs on ovh-n0001
  - landed in wallet's local DB with correct lifecycle (`unproven` → `unmined` → `completed`)
- Negative test: deliberately-malformed BEEF returns proper rejection through arcade and is NOT admitted by overlay (post-G)
- Paymail end-to-end: send/receive paymail txs via 1sat-stack, confirm REJECTED branch still surfaces correctly

## Decisions needed before starting

1. Standalone arcade (memory kafka, pebble store) acceptable for ovh-n0001, or do we want real Kafka + Aerospike split deployment?
2. Reverse-proxy `/arcade/...` (Item E.i) or break URL contract (E.ii)?
3. SSE callback token: fixed token in 1sat-stack config, or per-instance random?
4. Paymail poll-after-submit: default timeout, and on timeout do we 202 or 5xx?

## Out of scope

- Arcade clustered/HA: real Kafka, Aerospike, separate merkle-service. Defer until standalone deployment is proven.
- `GetPolicy` HTTP route: arcade main doesn't expose; 1sat-stack doesn't currently call it.
- Recovering existing failed deploys (`d1d98de0`, `181baec`): create new test deploys post-migration.
- go-sdk Arc broadcaster classification adjustments for 202: handle in Item G if needed; may require a small upstream PR.
