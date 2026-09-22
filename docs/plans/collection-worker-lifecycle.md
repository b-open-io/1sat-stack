# Collection Worker Lifecycle & Payment Discovery

Status: **Not Started**

Design settled in review on rack (2026-09-22), on top of the Postgres overlay
backend from `docs/handoff/2026-09-21-collection-dev.md`. Two coupled problems:
item workers do not scale with collection count, and funding detection polls
addresses it cannot enumerate. Fixing payments removes one polling loop; this
plan removes the rest.

## Problem 1: workers exist per eligible collection

Verified in code (`pkg/collection/manager.go`, `pkg/overlay/sync.go`):

- The manager reconciles eligible collections (whitelisted, funded, or
  `index_all`) and starts **one goroutine per collection**, each running
  `OverlaySync` forever.
- Each sync worker polls its own queue (`q:tm_col_{id}`) with a 1s
  `PollDelay` when the queue is empty.
- `sync.item_workers: 2` is a shared limiter channel on the processing step.
  It caps items **in flight**; it does not cap workers that **exist**.

The mss1 stress run showed the shape: 434 collections → 416 workers, 578 open
fds, 1.6 GB RSS, ~420 empty queues polled every second. Postgres removed the
per-collection storage wall (files, fds), but not the polling wall. At 2,000
collections that is 2,000 pollers; at 10,000, 10,000 pollers doing roughly
864M no-op queue reads per day against the queue store, plus a reconcile tick
that computes status per collection.

Idle cost must be proportional to collections in **data**, not in goroutines,
pollers, file handles, or topic-manager registrations.

## Problem 2: fee addresses cannot be enumerated

Funding is checked by pulling UTXOs for each collection's fee address
(`ownerSync`, `pkg/sync/addresssync.go`) on processed-item events and a
15-minute `refreshInactive` sweep. The addresses are BRC-42 derivations
(identity key `pkg/collection/fee.go`, invoice = collection outpoint
`txid_vout`, anyone counterparty). One address per counterparty, open-ended
address space: not enumerable ahead of time, so not subscribable on JungleBus
by address pattern. The 15-minute sweep exists only because of this.

## Design: payment discovery

**Tag payment txs with a bitcom envelope.** Payment txs may carry, in addition
to the real P2PKH output to the derived fee address:

```
OP_FALSE OP_RETURN
  <fee identity address>     ; 1Sat5rCg1TwWeC9hVG1tMMfNFza59rDmq, protocol ID
```

No payload: collection attribution stays exactly as today — match the paid
output against the derivation of a known collection's invoice. The OP_RETURN
is only a "this is a collection payment" beacon so **one** JungleBus
subscription with pattern `^1Sat5rCg1TwWeC9hVG1tMMfNFza59rDmq` delivers every
tagged payment ever made. More fields after the protocol ID are additive and
reserved (invoice echo, payer ref); nothing needs them yet.

**The tag is an unauthenticated claim.** Anyone can push the protocol ID
without paying. On receipt the node verifies against the tx itself: does an
output actually pay the BRC-42-derived fee address of some invoice? Verified →
internalize + credit. Not verified → ignore. The feed brings candidates; the
node validates. Same trust model as every other overlay.

**Feed payments into the normal internalizer.** A tagged tx is parsed and its
outputs written to the local output store keyed by the fee address, like any
other ingestion. Consequences:

- Balance/credit checks (`Debits()`, `GetCollectionStatus`) become **local
  queries** — no JungleBus or ownerSync round-trip in the hot path. Cheap
  enough to run on demand, e.g. per web request.
- Live (tag-fed) and backfilled (swept) payments land as identical rows; the
  credit logic cannot diverge and re-sweeps are idempotent.
- Attribution is by *valid derivation of the fee identity key*, not "known
  collection", so a payment that arrives before its collection is discovered
  still gets indexed; discovery joins it later.

**Backfill.** Payments broadcast without the tag are permanently invisible to
the subscription. One batched address sweep (existing ownerSync against the
fee addresses of known collections) credits them, then retires. Thousands of
derivations is trivial compute. This requires **fee address persisted per
collection** — add an indexed `fee_address` column to the Postgres collection
table. The column doubles as the sweep's work list.

**API payments get the tag for free.** Payers submitting through a server route
know what they paid; the server builds/verifies the tagged output. The tag is
optional precisely so the sweep can backfill strays.

Open verification item: confirm GorillaPool pattern matching accepts a full
base58 address as protocol prefix (BSV-21 proves the mechanism with a shorter
tag) and check pattern length limits, before implementing.

## Design: worker lifecycle

**The map is the truth; goroutines are rented.** The manager keeps the active
set as data (collection ID → status, in memory backed by Postgres) and holds
zero standing workers. A worker — goroutine + topic-manager registration +
drain loop (and in the SQLite backend, the DB connection) — exists only while
the collection has work.

Push sources that mark a collection ready:

1. **Dispatcher enqueue.** Items placed on `q:tm_col_{id}` mark the collection
   ready if eligible. The queue persists regardless of a running worker
   (already true today), so a topic that is asleep loses nothing.
2. **Payment internalized.** Balance crosses into funded and the queue is
   nonempty → ready. Event-driven; replaces the 15-minute `refreshInactive`.
3. **Web request.** `GET` on a collection serves straight from Postgres
   (always works, no worker needed) and marks the collection
   ready-with-extended-idle if it has backlog. Serve stale-while-refreshing;
   eventual consistency is acceptable here.
4. **Admin actions** (whitelist add, blacklist, force) mark ready/cancel
   directly, as they effectively do now.

Execution: a **pool of drain goroutines** claims ready collections, drains the
queue until empty (or budget exhausted — see knobs), then releases: unregister
topic manager, drop rented resources, remove from the running map. The
5-minute reconcile remains only as a janitor for drift (manual config edits,
reorg cleanup), batched to O(1) queries rather than per-collection status
computation.

**Two knobs replace one.** `item_workers` becomes *collections draining
concurrently* — can be large (32–64), because drain work is mostly reads
(Kvrocks BEEF gets, decode, engine validation) and Postgres writes. The
existing shared limiter keeps a small number but changes meaning to
*broadcast/admission concurrency* (ARC courtesy); it is the value that stays
conservative (2–4). Postgres pool size (32 today,
`pkg/overlay/storage/postgres_factory.go`) becomes the next tuning surface;
connection-pool queueing degrades gracefully, unlike idle pollers.

**Drain policy:** start with drain-to-empty; if one whale collection with a
huge queue can starve the pool, add budget-slice re-queue. Do not build it
before it is needed.

At 10,000 funded collections: 10,000 map entries (bytes each), a handful of
live drainers, no standing pollers. The only continuous pollers left anywhere
are the ingest queue and the dispatcher, which already exist.

## Out of scope

- Queue store move Badger → Kvrocks (independent decision; the lifecycle
  design is store-agnostic).
- Funding rules for other overlays — collections only, for now.
- Putting BEEF bytes or queues in Postgres (handoff rule: do not).

## Sequencing

1. `fee_address` column + backfill sweep over known collections.
2. Bitcom-tag verification + internalization on a second JungleBus
   subscription; local balance queries; delete `refreshInactive` polling.
3. Ready-set + drain pool replacing standing per-collection workers; split
   `item_workers` / broadcast limiter.
4. Web-request and payment wake hooks.

Steps 1–2 remove address polling; step 3 removes queue polling; step 4 closes
latency. Each step is independently deployable on the rack testbed
(`deploy/compose.yaml`, see handoff 2026-09-21 rack section).

## Verification

- Unit: manager ready-set transitions (enqueue/payment/request/idle-expire),
  tag verification accept/reject cases.
- Integration on rack: fund a collection end-to-end — external payment with
  tag arrives via subscription, credit recalculates from local store, worker
  spins up, drains, unregisters; `GET /1sat/collections/{id}` serves during
  all of it.
- Scale sanity: synthetic ready-set of 10k eligible collections, confirm
  steady-state goroutine count ≈ pool size + a few, and no queue polling.
