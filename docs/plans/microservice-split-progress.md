# Microservice Split — Overnight Execution Log

Branch: `microservice-split` (from `master` @ `ab77548`). Running the plan in
`microservice-split-implementation.md` milestone by milestone: implement → review → fix-loop →
next. This log is the morning-review summary; newest status at the bottom of each milestone.

## ⚠ Finding needing your decision (NOT blocking — M4/M5 are independent)

**The stale-rollback branch is dead code in production — stale unconfirmed txs are NEVER rolled
back (from txo *or* overlay).** Verified: `beef.Storage.UpdateMerklePath`
(`pkg/beef/beef.go:358`) returns a plain `errors.New("unable to fetch…")` on a genuine miss —
never `beef.ErrNotFound`, never `(nil,nil)`. In `pkg/indexer/pending_auditor.go`
`processUnconfirmed`, that classifies as `isError` → "retry next block" → the
`score <= rollbackCutoff` rollback branch is unreachable. So a tx that's broadcast + admitted to an
overlay but never confirms (dropped / double-spent / underfunded) and never gets an explicit
arcade REJECTED stays in `tx:pending` forever, and its outputs persist in both txo and overlay
storage indefinitely. This is the actual root of the phantom-output/`davidt` problem — larger than
my original findings doc claimed (I wrote "auditor rolls back txo but not overlay"; in fact it
rolls back neither).

**Impact on M3:** the Task 3.1 fix (auditor publishes a rollback event → overlay also cleans up)
is correct and unit-tested, but **inert in production** until the classification bug is fixed.
The M3 *split* deliverables (overlays carry no txo, adapter ingest unwired) are unaffected and sound.

**Why I did NOT fix it autonomously:** the fix — make `UpdateMerklePath` signal genuine-not-found
distinctly (return `beef.ErrNotFound`), or reclassify that specific error in `processUnconfirmed` —
*enables rollback of transactions that currently never roll back*. That is a destructive-direction,
timing-sensitive behavior change (a legitimately-slow tx wrongly rolled back would delete live
outputs), gated by `rollbackAge`, in exactly the area you care most about. Needs your call on
whether/how to enable it and whether `rollbackAge` is safe. Pre-existing bug, orthogonal to the
split — I'm continuing M4/M5 and leaving this for you.

## Resolved decisions (discussed and settled)

- **Decision 9 / overlay ingest (RESOLVED with David).** Overlay modes carry NO txo/indexer/
  auditor. The overlay adapter `IngestTx` stays unwired in ALL modes (including `mode: all`);
  overlay-submitted txs are ingested via the broadcast→arcade→arc-status feedback loop
  (`handleAccepted` indexes, `handleMined` folds in the proof). This keeps `mode: all`
  behaviorally identical to a split deployment and to master. The plan's original M3 Task 3.2
  Step 1 ("wire IngestTx when index runs") was a bug in the plan text — it would double-ingest;
  corrected in commit `docs: correct M3 Task 3.2`. My earlier "double-ingest" alarm was me
  rediscovering that the plan step contradicted our own decision 9.
- **Mode-dependency validation (RESOLVED — no work needed now).** Dependency checks already
  exist: each service's `Initialize` validates its deps and returns a readable error (e.g.
  `pkg/paymail/config.go:97`), wrapped and propagated by the wiring, so an invalid single mode
  fails to boot with a message naming what's missing. Only polish is phrasing errors in mode
  terms — deferred to M5. (Note: paymail is being refactored out of the stack separately; not
  part of this delivery — I'll stop using it as an example and won't treat it as load-bearing.)

## Decisions I made autonomously (review these first)

1. **M1 parity correction (commit `d1d6111`).** The plan's Task 1.3 specified a call-time
   `IngestTx` closure for the overlay engine. On verifying against master, that closure would
   **newly enable** overlay-submitted transactions to trigger general txo indexing — a path
   master left dormant because its init ordering captured `OutputStore.IngestTx` as nil before
   the indexer set it (master `cmd/server/config.go`: overlay init at :988, IngestTx assigned at
   :1226; adapter skips on nil at `pkg/overlay/storage/adapter.go:58`). That is a runtime
   behavior change (with double-ingest risk) inside a milestone whose contract is byte-for-byte
   parity with master, and it is exactly the overlay→index feedback loop **Milestone 3** is meant
   to wire deliberately. **I reverted to strict parity**: overlay adapter `IngestTx` stays nil in
   `mode: all`, deferred to M3. The capabilities parity diff can't catch this (route surface is
   unchanged) — it was caught by reading the wiring. **If you'd rather ship the enable now, that's
   an M3 conversation; I chose the conservative path for an unattended run.**

## Milestone status

### M1 — pkg/node + mode selection — COMPLETE
- Implemented (commits `cd8ad77`, `ae709f2`, `7f59561`) + parity fix (`d1d6111`).
- Gate: `go test ./...` green; `mode: all` capabilities diff vs master baseline **empty**
  (re-verified after the parity fix on port 18080); `mode: opns` boots with only its surface.
- Review verdict: **sound**. Function-body diffs of Initialize/Close/RegisterRoutes/
  StartSubscribers/applyRuntimeConfig/SetDefaults/LoadConfig confirmed byte-identical to master
  except intended additive changes. Parity fix independently confirmed correct (the plan's
  originally-suggested lazy closure would have broken parity by double-triggering ingest).
- Plan deviations (benign): docs endpoint is `/1sat/api-spec/swagger.json`; paymail uses `Mode`
  not `Enabled`; `convertSpendsChain` moved alongside `convertBeefChain`.

**Findings deferred for your signoff / later milestones (none block M1 parity):**
1. **Legal mode combinations need defining (design decision — your call).** `mode: paymail` alone
   can't boot: `paymail.Initialize` hard-requires OpnsLookup/Ordfs/BroadcastHandler, which are nil
   unless index+opns+ordfs also run. Today it fails with a dep error, not a clear config error.
   Options: composer validates mode sets and errors clearly / auto-pulls required co-services /
   documents co-location rules. Decision 2 never enumerated inter-service mode deps. I did not
   invent a rule — needs your direction. Relevant to M5 (gateway/deployment).
2. **`mode: all` implicitly assumes index enabled.** Broadcast `/tx` and spends are now
   `runsService("index")`-gated (intended, decision 2). A `mode: all` config with txo+indexer
   *disabled* would drop `/tx`+spends vs master — an edge the capabilities gate can't see (baseline
   has index on). Realistic configs default txo embedded, so parity holds. Informational.
3. **admin/sweep/landing ungated** → subset modes (e.g. `mode: opns`) also serve the admin UI.
   Consistent with decision 2 (admin deferred to M5); M5's per-service admin work resolves it.

### M2 — Queue interface — COMPLETE
- Commits `30ed013` (interface + StoreQueue + Config), `3349791` (migrate all `q:*`
  producers/consumers), `ff4d894` (marker).
- Review verdict: **sound**, no Critical/Important findings. Semantic fidelity of
  Read/Ack/Requeue/Depth vs master's worker loop confirmed byte-equivalent; GASP `MinExclusive`
  drop confirmed inert (keyed remote is dead code; live `BeefRemote` sites pass empty keys on
  master too); `q:*` fully migrated; `tx:*` logs correctly left on the store; Close ordering
  correct (inherit = no-op, dedicated closes its own store once).
- **Known interface gap (deferred, tied to out-of-scope gap 7):** `queue.ReadCfg.From` is
  inclusive-only and cannot express the `> since` cursor a keyed GASP remote needs. Does NOT go
  live in M1–M5 (the keyed `BeefRemote`/`QueueGASPRemote` is dead code here; the SSE+GASP feed is
  the opns-overlay RemoteConfig work, gap 7, separate repo). Whoever wires that path must add an
  exclusive-`From` option to `ReadCfg` first, else `>= since` re-emits every txid at the max
  block-height score each round (wasted re-traversal, not corruption). Left additive-on-demand.
- `pkg/queue`: `Queue` (Enqueue/Read/Ack/Requeue/Depth/Close), `StoreQueue` over the main store's
  `q:*` keys, `Config` (`provider: inherit|store`). Migrated worker, jbsync, indexer sync,
  overlay (p2p/event_bridge/sync/topic/services), gasp remotes, bsv21. `pkg/node` builds
  `svc.Queue` after the store, threads it through, closes it before the store.
- Gate (implementer-reported): `go test ./...` green; parity diff empty; `mode: opns` boots;
  dedicated-backend smoke passes (item lands in dedicated badger path, absent from main store).
- Under review — focus: semantic fidelity of Read/Ack/Requeue vs master's worker loop, and the
  flagged GASP `MinExclusive` drop (implementer claims inert; being independently verified).
- Deviations to confirm: indexer sync/config migrated (not in task list); GASP `GetInitialResponse`
  dropped `MinExclusive` (inclusive `From` in Queue model); dedicated queue store defaults to a
  `queue` badger path to avoid colliding with the main store dir.
### M3 — Event-driven lifecycle — implemented, under review
- Commits `b545ba4` (auditor publishes stale rollbacks), `7676de2` (overlay modes run without
  txo), `7e9df2d` (marker).
- Task 3.1: auditor publishes a synthetic "arc" REJECTED on stale rollback → overlay storage
  rolls back via `handleRejected`. Correct + unit-tested (`TestProcessUnconfirmedPublishesStale
  Rollback`) — but see the ⚠ finding above: the rollback branch is dead in production, so this is
  inert until the separate classification bug is fixed. The implementer also extracted a minimal
  `beefProofStore` interface for the auditor (backward-compatible; `*beef.Storage` satisfies it).
- Task 3.2: txo/indexer/auditor gated on `runsService("index")`; adapter IngestTx stays nil in all
  modes; overlay-only processes wire the status loop (arc bridge + StatusHandler, `ingest:false`,
  no txo pieces). `mode: opns` builds no txo/indexer/auditor; `mode: all` full pipeline intact.
- Gate (implementer-reported): auditor test green; full suite green; `mode: all` capabilities diff
  empty; `mode: index` and `mode: opns` boot with correct surfaces.
- Review verdict: split work (Task 3.2) **sound** — overlays carry no txo, adapter unwired,
  gating correct, parity intact, StatusHandler nil-safe for overlay-only processes. Task 3.1
  publish is correct + consumed by `handleRejected`, but **confirmed dead in production** (the ⚠
  finding). Added `TestProcessUnconfirmedFetchErrorDoesNotRollback` (commit `test: document…`) so
  the suite states the truth: under production error semantics no rollback/publish fires. That test
  should flip to expect a rollback once the classification bug is fixed.
- **M3 COMPLETE** with the caveat that R5 (stale→arc REJECTED→overlay rollback) is implemented but
  inert until the dead-branch bug is addressed (tracked as a task chip + the ⚠ finding; David
  acknowledged it needs tracking).
### M4 — Remote providers — implemented, under review
- Commits `c5d5ae6` (spends http provider), `9ed337d` (bsv21 owner sync/balance behind injected
  interfaces), `ec25679` (marker).
- spends: new `http` chain tier (`spends.chain[].http.url`) querying a remote index's `/txo` spend
  routes; read-only (Put/Delete no-ops). bsv21: dropped `pkg/indexer`+`pkg/owner` imports
  (`grep` empty), now takes injected `OwnerSyncer` + new `BalanceLookup`; embedded owner-sync moved
  to node wiring; remote mode (`bsv21.owner.mode=remote`+`url`) via new `pkg/bsv21/ownerclient.go`
  hitting `/owner/sync` + `/owner/:owner/balance`; embedded-without-index → clear startup error.
- Gate (implementer-reported): tests green; parity empty; `grep pkg/indexer|pkg/owner in pkg/bsv21`
  empty; **two-process smoke passed** — standalone `mode:bsv21` (owner.mode=remote) talking to a
  separate `mode:index` process; bsv21 has no txo/owner routes, index serves the balance endpoint
  in the exact shape bsv21's client decodes.
- Under review — priority: nil-txo deref safety in bsv21 (it now tolerates a nil txo store) + HTTP
  client encoding fidelity vs the real routes.
- **Known pre-existing quirk (confirmed, not introduced by this work):** the store's badger path
  opens CWD-relative because `Store.Initialize` runs before `resolveAllPaths` — identical ordering
  on master (`config.go:834` init vs `:857` resolve) and branch. Co-located multi-process deploys
  must run from distinct working dirs. Migration-sensitive (production data may live at CWD/store),
  so NOT fixed here — flag for the configurator/deployment work.
### M5 — Gateway + per-service admin — not started
