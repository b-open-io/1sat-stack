# Microservice Split — Overnight Execution Log

Branch: `microservice-split` (from `master` @ `ab77548`). Running the plan in
`microservice-split-implementation.md` milestone by milestone: implement → review → fix-loop →
next. This log is the morning-review summary; newest status at the bottom of each milestone.

## ⏸ STOPPED at the M2/M3 boundary — needs your decision before continuing

**Done overnight: M1 + M2, both fully implemented, independently reviewed (verdict sound), and
committed on `microservice-split`. `mode: all` parity with master verified at both milestones.**

I stopped rather than start M3 because M3 ("event-driven tx lifecycle") turns on a semantic
decision my own decision 9 left ambiguous, and I found the plan's Task 3.2 step gets it wrong in a
way that would introduce a double-ingest bug. This is production-indexer correctness — your call,
not a subagent's.

**The finding:** In the overlay engine's `Submit`, a live (non-historical) tx triggers BOTH
`Broadcaster.Broadcast` (engine.go:572) AND `Storage.InsertOutputs` → adapter → `IngestTx`
(engine.go:605). On master the adapter `IngestTx` is dead (nil, from the init-ordering quirk I
fixed for parity in M1), so overlay submissions are ingested only via the broadcast→arcade-SSE
lifecycle — and that works in production today. The plan's M3 Task 3.2 Step 1 says to wire
`IngestTx` whenever index runs; in `mode: all` that would ingest each overlay-submitted tx twice
(once immediately via the adapter, once when the arc SSE returns), and because
`SaveTransaction` re-publishes events (`pkg/txo/output_store.go:258`), the duplicate fans out to
every overlay queue. Wasteful churn, not corruption — but wrong.

**My recommendation for M3 (needs your ✓):** resolve decision 9 the master-consistent way — the
adapter `IngestTx` stays unwired; ingestion is driven by the broadcast→arc lifecycle in all
profiles. Overlay-only processes get their `Broadcaster` pointed at the index service's `/tx`
endpoint plus a `StatusHandler` consuming arc events for rollback. That makes M3 mostly additive
(Task 3.1 auditor-publishes-rollback + the overlay-only status loop) and drops the risky
"wire IngestTx when index runs" step entirely. If instead you want overlay submissions ingested
immediately (adapter path) with the broadcast path suppressed, that's a different design and we
should talk it through.

Also still needs your input (from M1 review, non-blocking): **legal mode-combination rules** —
`mode: paymail` alone can't boot (hard-requires opns/ordfs/index deps); should the composer error
clearly, auto-pull required co-services, or just document co-location? I didn't invent a rule.

Related: the stale-rollback hole fix is already on your task chips ("Fix stale-rollback hole");
M3 Task 3.1 is the same fix, so decide whether to do it there or standalone.

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
### M3 — Event-driven lifecycle — BLOCKED on decision (see top of doc)
- Not started. The plan's Task 3.2 Step 1 ("wire IngestTx when index runs") would double-ingest
  in `mode: all` — see the STOPPED section. Needs the decision-9 resolution before implementing.
- When unblocked, Task 3.1 (auditor publishes rollback events) is safe/additive; Task 3.2 needs
  rewriting to the master-consistent model (adapter IngestTx stays unwired; overlay-only processes
  broadcast to the index `/tx` + consume arc events).
### M4 — Remote providers — not started
### M5 — Gateway + per-service admin — not started
