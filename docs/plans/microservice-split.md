# Microservice Split — Discovery & Decisions

Status: **In Progress**

Supersedes [docker-compose-service-split.md](./docker-compose-service-split.md). That plan kept a
"core" super-app and had overlays reaching into core's store over `/internal` APIs; the decisions
below replace that shape. Its compose-generation design (Phase 3–5) remains relevant for the
configurator and should be revisited once the mode pattern lands.

## Goal

1sat-stack becomes primarily a library plus one binary that can run any subset of its services.
No deployment runs "everything because it's one process." Standalone single-box deployments keep
working with zero external infrastructure; shared infrastructure (api.1sat.app) runs each service
as its own process pointing at shared providers.

## Decisions

1. **One binary, arcade-style `mode` selection.** Same pattern as arcade's `mode: all |
   api-server | chaintracks | ...` (`arcade/config/config.go:101`, `arcade/app/app.go:307-475`).
   `mode: all` mounts every enabled service in one process on one port — identical behavior to
   today's monolith, which it replaces. Any single mode runs just that service. Mode accepts a
   set (`mode: index,opns`) to co-locate services in one process. Docker Compose in production is
   just the same image run N times with different modes.

   Mode interacts with per-module enabled/disabled config as follows: `mode: all` runs
   everything that is *enabled* — the existing per-module flags, their defaults, and the admin
   UI toggles keep their current meaning, so a standalone user configures nothing new. An
   explicit mode list implies enablement: naming a service turns it on, with no separate
   `enabled` flag to flip.

2. **Service inventory (modes):**
   - `gateway` — reverse proxy owning the public `/1sat/*` surface, merged `/capabilities` and
     OpenAPI docs, root-level routes (`/content`, `/.well-known/*`). Future home of edge
     auth/micropayment middleware. Only exists in multi-process deployments; `mode: all` never
     runs it.
   - `index` — jbsync + indexer + txo + owner + broadcast. txo and indexer form one inseparable
     storage domain (see findings). Serves the txo/owner/spends HTTP APIs, the `/tx` broadcast
     endpoint, and the SSE event stream.

     spends is *not* part of this domain: it is a separable resolution chain (store tier → LRU →
     JungleBus, `pkg/spends/spends.go:21`) like beef's provider chain. Its store tier reads the
     `spnd` hash that txo owns and writes, so that tier is only available when the deployment
     runs the index service (in-process, or via the spends HTTP provider once built). Without
     it, a consumer's chain is cache + JungleBus. Note: txo's internal paths deliberately bypass
     the chain to avoid JungleBus fallback in hot paths (`pkg/txo/output_store.go:300`).
   - `beef` — BEEF storage service.
   - `ordfs` — content serving.
   - `paymail` — paymail server.
   - One mode per overlay: `bsv21`, `opns`, `bap`, `bsocial`, `ordlock` (engine + lookup + sync
     worker + routes). Overlays never share a process with each other in production.
   - Admin UI placement deferred (see open questions).

3. **`store.Store` never becomes a network interface.** The keyspace audit showed it is txo's
   private database plus queues, not shared state. It stays private to the index service.
   Overlays end up with zero direct store access: their data lives in their per-topic engine
   storage (SQLite/Postgres) or Mongo, their queue moves behind the new Queue interface, and
   bsv21's one cross-service read goes over HTTP.

4. **Three data-movement abstractions, each with embedded and shared providers, each its own
   config section:**

   | Role | Interface | Embedded default | Shared provider |
   |---|---|---|---|
   | data at rest | `store.Store` | Badger | Redis (existing) |
   | durable work queues | `Queue` (new, carved out of store) | store-backed | broker URL |
   | live events / broker | `pubsub.PubSub` | channels | Redis (existing) |

   Queues today are sorted-set keys inside the store drained by polling workers
   (`pkg/worker/worker.go:21`), which welds queue sharing to store sharing. The `Queue`
   interface breaks that: a service can have a private store and a shared queue backend.
   Config never names a specific backend in module code.

   Interface shape: codify current semantics — scored enqueue, paged read in score order
   (block-height ordering is load-bearing), ack-by-removal, re-score for retry — with
   single-consumer-per-queue as a documented invariant. No leases/competing consumers until a
   real scaling need exists; they can be added behind the same interface. First implementation
   is a `StoreQueue` wrapping the existing sorted-set ops (same `q:*` keys, no migration); the
   queue's store handle is configured independently of the service's data store.

5. **Inter-service feeds, chosen by trust boundary:**
   - Same server/infrastructure: producers and consumers point at the shared queue backend.
     Durable; overlay down for an hour resumes from its backlog. This is how the system already
     behaves, minus the abstraction.
   - Across the internet: SSE + GASP via the existing per-topic `RemoteConfig`
     (`pkg/overlay/services.go:21`), which creates a GASP remote (pull/backfill) and an
     `SSEListener` (`pkg/gasp/sse_listener.go`) filling the local queue. This is the standard
     feed for external standalone overlays (opns-overlay pattern). opns-overlay currently
     hardcodes a 5-minute GASP poll instead; it should adopt RemoteConfig.

6. **bsv21 becomes a clean leaf.** Its only entanglement with `indexer`/`owner` is fee
   accounting: sync the fee address (`pkg/bsv21/manager.go:218,365`) then read its unspent
   balance (`pkg/bsv21/worker.go:105-115`). Both operations already exist as HTTP endpoints on
   the index service (`pkg/owner/routes.go:41-43` — `/sync`, `/:owner/balance`). bsv21 already
   consumes the syncer through its own `OwnerSyncer` interface (`pkg/bsv21/manager.go:25`);
   an HTTP-backed implementation replaces the direct `owner.NewOwnerSync` construction and the
   `indexer`/`owner` imports drop. In `mode: all` the wiring may still hand it in-process
   implementations.

7. **One canonical wiring implementation.** A composition package (working name `pkg/node`)
   owns the root config (aggregating the existing per-package config sections, same
   `SetDefaults` cascade and `ONESAT_*` keys) and builds the substrate in the canonical
   dependency order — each dependency embedded or remote per that config — then mounts the
   selected modes' registrations. `cmd/server` becomes a thin caller; external overlay repos
   (opns-overlay) call it instead of hand-copying the init sequence. This removes the
   copy-drift class of bug (see findings). `node.Compose` takes a fully-resolved config; it
   has no knowledge of which layers (viper, config.db, injected env) produced it.

8. **config.db is the deployment's config, not each process's.** The admin UI remains the way
   a user shapes their system — users never hand-maintain the full option surface in YAML.
   YAML/env stays bootstrap-only (data dir, port, key).
   - Standalone (`mode: all`): one process reads config.db, admin UI edits it — unchanged.
   - Multi-process: config.db is the input to the configurator, which renders the deployment
     (enabled services, providers, URLs) into docker-compose with per-service resolved config
     injected as env vars. Worker services are dumb: no config.db, no admin UI, boot from what
     they're handed. (Translation design reused from the superseded compose plan, Phases 3–4.)
   - The admin UI therefore moves from a mounted route inside a worker process to the control
     plane (alongside configurator/gateway). Broader frontend consolidation (sweep, overlay
     UIs as standalone apps) is explicitly out of scope here.

9. **Overlay processes do not ingest into txo; tx lifecycle is event-driven.** The overlay
   engine adapter's `IngestTx` callback (`pkg/overlay/storage/adapter.go:59-72`) — which pushes
   every overlay-submitted tx through general txo indexing so the pending/audit lifecycle can
   track it — becomes index-service wiring only. Overlay processes: broadcast through their
   configured `Broadcaster` (the `/tx` endpoint — the index service in split mode, which does
   its own ingestion there), and consume tx-status events for cleanup. Rollback execution is
   per-data-owner: each consumer of the status stream rolls back its own storage
   (`handleRejected` already does exactly this for overlay topic storage). The PendingAuditor
   becomes a *producer* of rollback events on the broker instead of reaching into storage it
   doesn't own — closing the stale-rollback hole (see findings) with the same mechanism in
   both profiles (in-process pubsub in `mode: all`, SSE across processes). Net effect: overlay
   modes carry no txo, no indexer pipeline, no auditor.

10. **Per-service admin routes.** The shared `admin/` package's in-process reach into every
    module is replaced by each service exposing its own admin-guarded routes (index: data
    browser/queue endpoints; bsv21: whitelist/token controls; opns: crawl trigger; etc.),
    mounted via the registrar like their other routes. The admin UI remains one control-plane
    frontend but becomes a pure API client aggregating those per-service surfaces. Its
    config-editing core is unaffected (config.db is control-plane-local, decision 8). In
    `mode: all` every admin route is mounted in the one process — no behavior change. The
    per-service endpoint inventory happens during implementation, alongside the admin UI
    refresh it needs anyway.

11. **Embedded defaults everywhere.** A standalone deployment (any mode, including `all`)
   requires no external infrastructure: Badger, SQLite, in-process channels, filesystem beef.
   Shared providers are opt-in config. Provider chains already support the hybrid (e.g. local
   filesystem beef tier + HTTP tier against a shared beef service).

## Findings that shaped the decisions

- **`mode: remote` is a stub in most packages.** store (`pkg/store/config.go:79`), beef
  (`pkg/beef/config.go:114`), txo (`pkg/txo/config.go:95`), bsv21 (`pkg/bsv21/config.go:161`),
  pubsub (`pkg/pubsub/config.go:79`) all return "not yet implemented". Working remote paths
  today: chaintracks mode/URL, arcade client, beef HTTP tier (`pkg/beef/http.go`), GASP HTTP
  remotes, ordfs HTTP client, paymail messagebox client.
- **Keyspace audit.** The `ev:`/`{outpoint}`/`sats`/`spnd`/`tx:*` keys are written only by
  txo and indexer; the `tx:pending` lifecycle spans txo ↔ indexer, making those two one
  storage domain — hence the single `index` service. txo owns the `spnd` hash; spends'
  store tier reads it as a data source (the literal is duplicated at `pkg/txo/keys.go:18`
  and `pkg/spends/store.go:11` — should become a shared constant). Overlay modules touch
  the store only via their `q:{topic}` queues and one read path (bsv21 balance).
- **Wiring duplication already caused a production bug.** opns-overlay's first hand-copied
  wiring omitted the txo/indexer/status-handler layer; arcade REJECTED events were dropped and
  rack served the OpNS name `davidt` at a rejected tx until
  `opns-overlay/docs/plans/2026-07-13-wrap-stack-real-services.md` re-synced the wiring. Only
  ~6 lines of opns-overlay's ~150-line bootstrap are OpNS-specific.
- **The route layer is already split-ready.** Every service registers routes prefix-free via
  `pkg/registrar` with per-service OpenAPI fragments; any subset mounts independently. The only
  aggregate concerns are `/capabilities` and merged docs, which move to the gateway.
- **Queues are multi-producer.** jbsync, the overlay P2P bus, event bridges, and GASP SSE
  listeners all produce into `q:{topic}`; one OverlaySync consumer each. The queue backend is
  shared infrastructure in any multi-process deployment.
- **Stale-rollback hole (live defect, pre-existing).** The rollback paths are split by data
  owner: `StatusHandler.handleRejected` (`pkg/indexer/status_handler.go:325`) rolls back
  overlay topic storage only, on explicit arcade REJECTED events; the PendingAuditor's stale
  sweep (`pkg/indexer/pending_auditor.go:324-342`) rolls back txo only and emits no event —
  txo's `Rollback` publishes nothing, so `handleRejected` never fires for stale txs. A tx
  admitted to an overlay topic that never confirms (or whose REJECTED event was missed) is
  cleaned out of txo silently while the overlay keeps phantom outputs forever. Same class as
  the opns-overlay `davidt` incident, on the stale path. Warrants a fix independent of the
  split; the fix (event-driven rollback, below) is the same mechanism the split needs.

## New work identified (gap list)

| # | Item | Notes |
|---|---|---|
| 1 | `Queue` interface + embedded/shared providers | Carve out of `store.Store`; migrate `pkg/worker`, jbsync, event bridge, GASP listeners, OverlaySync |
| 2 | Composition package (`pkg/node`) | Canonical wiring; `cmd/server` and external overlays consume it |
| 3 | `mode` selection in the stack binary | arcade pattern; `mode: all` default preserves current behavior |
| 4 | `gateway` service | Path proxy, capabilities/docs merge, root-route mapping; later auth/payments |
| 5 | spends HTTP provider | Tier that queries a shared instance's `/txo/spends` endpoints, same shape as beef's HTTP tier |
| 6 | bsv21 HTTP `OwnerSyncer` + balance client | Against index service's owner routes; drop `indexer`/`owner` imports from bsv21 |
| 7 | opns-overlay: adopt RemoteConfig SSE feed | Replaces hardcoded 5-minute GASP poll; separate repo |
| 8 | Configurator (compose generation) | Later phase; reuse design from superseded plan |
| 9 | Event-driven rollback | PendingAuditor publishes rollback events on the broker; status consumers roll back their own storage. Closes the stale-rollback hole — candidate for a standalone fix before the split |
| 10 | `IngestTx` becomes index-only wiring | Overlay modes drop txo/indexer/auditor entirely (decision 9) |
| 11 | `overlay.InitializeDeps` takes `IngestTxFunc` instead of `*txo.OutputStore` | One-field change; removes the txo import from `pkg/overlay` (import hygiene) |
| 12 | Per-service admin routes + admin UI as API client | Endpoint inventory during implementation (decision 10) |

Delivered on branch `microservice-split` (M1–M5): gaps 1, 2, 3, 4, 5, 6, 9, 10, 11, 12.
Out of scope / separate effort: gap 7 (opns-overlay, separate repo), gap 8 (configurator).

## Parked / out of scope

- **bsv21 metering split** (separate commercial service registering/deregistering tokens with a
  general-purpose bsv21 overlay): viable — the whitelist path already runs tokens with no fee
  logic — but deferred as premature. bsv21 keeps its fee accounting.
- Kubernetes, service discovery, horizontal scaling of the index service.

## Open questions

None — the initial open questions have all been resolved into decisions (composition config →
decisions 7–8; queue semantics → decision 4; overlay/txo coupling → decisions 9 and gap 11;
admin UI → decision 10). New questions raised during implementation planning go here.
