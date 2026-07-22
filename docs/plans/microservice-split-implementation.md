# Microservice Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure 1sat-stack into a mode-selecting binary per `docs/plans/microservice-split.md` (decisions 1–11): canonical wiring in `pkg/node`, `mode` selection, `Queue` abstraction, event-driven tx lifecycle, remote providers for spends and bsv21 owner/balance, and a gateway service — with `mode: all` behaving identically to today's server at every milestone boundary.

**Architecture:** Five milestones, each ending in a working, committed state. M1 moves wiring into the library and adds mode selection (parity gate). M2 carves the Queue interface out of the store. M3 makes tx-status rollback event-driven and removes txo from overlay modes. M4 adds HTTP providers (spends, bsv21 owner/balance). M5 adds the gateway and redistributes admin routes to owning services. The configurator (compose generation) is explicitly out of scope.

**Tech Stack:** Go 1.26, gofiber/v2 (+ `middleware/proxy` in M5), viper, existing 1sat-stack packages.

**Branch:** all work on feature branch `microservice-split`, branched from `master`. Commit after every task. Never force-push. Run `gofmt -s -w . && go vet ./...` before every commit.

**Global rules for the executing agent:**
- After every task: `go build ./...` and `go test ./...` must pass before committing.
- Never leave comments describing what code used to do or where code was moved from.
- If a step's assumption doesn't match the code (line numbers drifted, signature differs), read the file and adapt — do not force the literal text in. Line refs were valid as of 2026-07-22.
- Milestone gates are mandatory: do not start milestone N+1 until milestone N's gate passes.

---

## Milestone 1 — `pkg/node`: canonical wiring + mode selection

Outcome: `cmd/server` is a thin caller of `pkg/node`. `mode: all` (default) is byte-for-byte behavior parity with master. Explicit mode lists select service subsets.

### Task 1.0: Capture parity baseline (before any changes)

**Files:** none (scratch output only)

- [ ] **Step 1:** On master, build and start the server with a throwaway data dir:

```bash
cd /path/to/1sat-stack
go run ./cmd/server --data-dir /tmp/split-baseline &
sleep 20
curl -s http://localhost:8080/1sat/capabilities | jq -S . > /tmp/baseline-capabilities.json
curl -s http://localhost:8080/1sat/docs/openapi.json | jq -S 'keys' > /tmp/baseline-docs-keys.json
kill %1
```

- [ ] **Step 2:** Create the feature branch:

```bash
git checkout -b microservice-split
```

### Task 1.1: Move wiring from `cmd/server` into `pkg/node`

**Files:**
- Create: `pkg/node/config.go`, `pkg/node/wiring.go`, `pkg/node/routes.go`, `pkg/node/subscribers.go`
- Modify: `cmd/server/main.go`
- Delete: `cmd/server/config.go`
- Move: `cmd/server/config_test.go` → `pkg/node/config_test.go`, `cmd/server/docs_test.go` → `pkg/node/docs_test.go`

- [ ] **Step 1:** Create `pkg/node` and move ALL declarations from `cmd/server/config.go` into it, split as:
  - `pkg/node/config.go`: `Config` struct, `SetDefaults`, `LoadConfig`, `applyRuntimeConfig`, `resolvePath`, `resolveAllPaths`, `convertBeefChain`.
  - `pkg/node/wiring.go`: `Initialize`, `Close`, `createOverlayP2PBus`, and every helper `Initialize` calls.
  - `pkg/node/routes.go`: `RegisterRoutes`, `moduleMounts`, `prefixOr`, and the per-service docs imports.
  - `pkg/node/subscribers.go`: `StartSubscribers`, `StartEventHandlers` and their helpers.

  Package name `node`. Every identifier that `cmd/server/main.go` uses must be exported (most already are: `Config`, `LoadConfig`, `Initialize`, `RegisterRoutes`, `StartSubscribers`). Unexported helpers stay unexported. Do not change any logic in this task — it is a mechanical move.

- [ ] **Step 2:** Update `cmd/server/main.go`: add import `"github.com/b-open-io/1sat-stack/pkg/node"`, replace references (`LoadConfig(...)` → `node.LoadConfig(...)`, `*Config` → `*node.Config`). Delete `cmd/server/config.go`. Move the two test files to `pkg/node/` and fix their package clause to `node`.

- [ ] **Step 3:** Verify and commit:

```bash
go build ./... && go test ./pkg/node/... && go test ./...
gofmt -s -w . && go vet ./...
git add -A && git commit -m "refactor: move server wiring into pkg/node"
```

- [ ] **Step 4:** Parity check: repeat Task 1.0 Step 1 against this branch into `/tmp/branch-capabilities.json`; `diff /tmp/baseline-capabilities.json /tmp/branch-capabilities.json` must be empty.

### Task 1.2: Mode selection

**Files:**
- Create: `pkg/node/modes.go`, `pkg/node/modes_test.go`
- Modify: `pkg/node/config.go` (Config struct + SetDefaults), `pkg/node/wiring.go` (one call in Initialize)

- [ ] **Step 1: Write the failing test** in `pkg/node/modes_test.go`:

```go
package node

import "testing"

func TestApplyModesAll(t *testing.T) {
	c := &Config{Mode: "all"}
	// mode=all must not touch any service flags
	before := c.OPNS.Mode
	c.applyModes()
	if c.OPNS.Mode != before {
		t.Fatal("mode=all must not modify service config")
	}
}

func TestApplyModesExplicitList(t *testing.T) {
	c := &Config{Mode: "index,opns"}
	c.applyModes()
	if !c.runsService("index") || !c.runsService("opns") {
		t.Fatal("named services must run")
	}
	if c.runsService("bsv21") || c.runsService("gateway") {
		t.Fatal("unnamed services must not run")
	}
	// naming a service implies enablement
	if c.OPNS.Mode == "disabled" || c.OPNS.Mode == "" {
		t.Fatal("naming opns must imply enablement")
	}
}

func TestRunsServiceAllHonorsEnabledFlags(t *testing.T) {
	c := &Config{Mode: "all"}
	c.BSV21.Mode = "disabled"
	c.applyModes()
	if c.runsService("bsv21") {
		t.Fatal("mode=all runs only enabled services")
	}
}
```

- [ ] **Step 2:** Run: `go test ./pkg/node -run TestApplyModes -v` — expect FAIL (undefined: applyModes).

- [ ] **Step 3:** Implement in `pkg/node/modes.go`. Service names and what they gate:

| name | gates |
|---|---|
| `index` | Indexer, TXO routes, Owner, Spends, Broadcast handler/routes, JungleBus subscribers |
| `beef` | beef HTTP routes (beef *storage* is substrate, always built per config) |
| `ordfs` | ORDFS service + `/content` |
| `paymail` | paymail service |
| `bsv21`, `opns`, `bap`, `bsocial`, `ordlock` | that overlay module + its routes + its sync workers |
| `gateway` | gateway service (Milestone 5; the name is reserved now) |

```go
package node

import "strings"

var serviceNames = []string{"index", "beef", "ordfs", "paymail", "bsv21", "opns", "bap", "bsocial", "ordlock", "gateway"}

func (c *Config) modeSet() map[string]bool {
	set := map[string]bool{}
	for _, m := range strings.Split(c.Mode, ",") {
		if m = strings.TrimSpace(m); m != "" {
			set[m] = true
		}
	}
	return set
}

// applyModes runs after applyRuntimeConfig. mode=all leaves per-service
// enabled flags authoritative. An explicit list implies enablement for the
// named services and disables everything else.
func (c *Config) applyModes() { /* per service: if !all && named → force
	embedded/enabled; if !all && !named → force disabled. Substrate sections
	(store, pubsub, beef storage, chaintracks, junglebus, arcade) untouched. */
}

func (c *Config) runsService(name string) bool { /* all → per-service enabled
	flag; explicit → membership */ }
```

Write the full bodies: `applyModes` switches on each service's config section (`c.Indexer.Mode`, `c.BSV21.Mode`, `c.OPNS.Mode`, `c.BAP.Mode`, `c.BSocial.Mode`, `c.OrdLock.Mode`, `c.Ordfs.Enabled`, `c.Paymail.Enabled`, beef/ordfs route flags). Add `Mode string \`mapstructure:"mode"\`` to `Config` and `v.SetDefault("mode", "all")` in `SetDefaults`. Call `c.applyModes()` in `Initialize` immediately after `applyRuntimeConfig`.

- [ ] **Step 4:** Gate every service block in `Initialize`, `RegisterRoutes`, and `StartSubscribers` with `c.runsService("<name>")` (wrapping the existing nil/enabled checks, not replacing them). Substrate blocks (store, config store, pubsub, junglebus client, p2p, chaintracks, beef storage, arcade client, txo OutputStore, overlay engine infra) are NOT gated — they are built whenever their config requires them or a running service needs them. Note: overlay modules need the overlay engine infra; `index` needs txo; keep the existing construction order.

- [ ] **Step 5:** Run: `go test ./pkg/node -run TestApplyModes -v` then `go test ./...` — expect PASS.

- [ ] **Step 6:** Boot check of a subset mode:

```bash
go run ./cmd/server --data-dir /tmp/split-m1-opns &
# with config: mode: "opns" via env: ONESAT_MODE=opns
sleep 15
curl -s http://localhost:8080/1sat/capabilities | jq -S .   # expect opns (+substrate) capabilities only
kill %1
```

- [ ] **Step 7:** Re-run the Task 1.1 Step 4 parity diff for default mode. Commit: `git add -A && git commit -m "feat: mode selection in node config"`.

### Task 1.3: `overlay.InitializeDeps` takes `IngestTxFunc` (drop txo import)

**Files:**
- Modify: `pkg/overlay/config.go:61-67,148-154`, `pkg/node/wiring.go` (the overlay Initialize call site)

- [ ] **Step 1:** In `pkg/overlay/config.go`, replace the field `OutputStore *txo.OutputStore` in `InitializeDeps` with `IngestTx overlaystorage.IngestTxFunc`. Replace the unwrap block at ~line 148 with direct assignment (`ingestTx := deps.IngestTx`). Remove the `pkg/txo` import.

- [ ] **Step 2:** In `pkg/node/wiring.go`, at the overlay `Initialize` call, pass `IngestTx: outputStore.IngestTx`-style wiring: since `OutputStore.IngestTx` is set *after* overlay init (ordering constraint), pass a closure that resolves at call time:

```go
IngestTx: func(ctx context.Context, tx *transaction.Transaction) error {
	if fn := outputStore.IngestTx; fn != nil {
		return fn(ctx, tx)
	}
	return nil
},
```

- [ ] **Step 3:** `go build ./... && go test ./...` → PASS. Verify: `grep -rn "1sat-stack/pkg/txo" pkg/overlay/` returns nothing. Commit: `git commit -am "refactor: overlay InitializeDeps takes IngestTxFunc"`.

### Milestone 1 GATE (mandatory)

- [ ] `go test ./...` green.
- [ ] Default-mode capabilities diff vs `/tmp/baseline-capabilities.json` is empty.
- [ ] `ONESAT_MODE=opns` boots and serves only its surface.
- [ ] Commit `chore: milestone 1 complete — node wiring + modes, parity verified`.

---

## Milestone 2 — `Queue` interface

Outcome: all queue producers/consumers go through `queue.Queue`. Default provider wraps the process's main store (same `q:*` keys — no migration). A queue can be configured with its own dedicated store backend.

### Task 2.1: Interface + store-backed implementation

**Files:**
- Create: `pkg/queue/queue.go`, `pkg/queue/store_queue.go`, `pkg/queue/store_queue_test.go`, `pkg/queue/config.go`

- [ ] **Step 1: Write the failing test** (`pkg/queue/store_queue_test.go`) using a temp Badger store via `store.NewBadgerStore` (see `pkg/store/badger.go` for the constructor signature):

```go
func TestStoreQueueRoundTrip(t *testing.T) {
	s := newTestBadgerStore(t) // helper: badger store in t.TempDir()
	q := queue.NewStoreQueue(s)
	key := []byte("q:test")
	ctx := context.Background()

	if err := q.Enqueue(ctx, key, queue.ScoredItem{Member: []byte("a"), Score: 1}, queue.ScoredItem{Member: []byte("b"), Score: 2}); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.Depth(ctx, key); n != 2 {
		t.Fatalf("depth = %d, want 2", n)
	}
	items, err := q.Read(ctx, key, queue.ReadCfg{Limit: 10})
	if err != nil || len(items) != 2 || string(items[0].Member) != "a" {
		t.Fatalf("read = %v, %v", items, err) // score order
	}
	if err := q.Ack(ctx, key, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := q.Requeue(ctx, key, queue.ScoredItem{Member: []byte("b"), Score: 99}); err != nil {
		t.Fatal(err)
	}
	items, _ = q.Read(ctx, key, queue.ReadCfg{Limit: 10})
	if len(items) != 1 || items[0].Score != 99 {
		t.Fatalf("after ack+requeue: %v", items)
	}
}
```

- [ ] **Step 2:** Run `go test ./pkg/queue -v` → FAIL (package missing).

- [ ] **Step 3:** Implement:

```go
// pkg/queue/queue.go
package queue

import "context"

type ScoredItem struct {
	Member []byte
	Score  float64
}

type ReadCfg struct {
	From  *float64 // inclusive; nil = -inf
	To    *float64 // inclusive; nil = +inf
	Limit int
}

// Queue is a durable, score-ordered work queue. Single consumer per key;
// multiple producers. At-least-once: items stay queued until Ack.
type Queue interface {
	Enqueue(ctx context.Context, key []byte, items ...ScoredItem) error
	Read(ctx context.Context, key []byte, cfg ReadCfg) ([]ScoredItem, error)
	Ack(ctx context.Context, key []byte, members ...[]byte) error
	Requeue(ctx context.Context, key []byte, item ScoredItem) error
	Depth(ctx context.Context, key []byte) (uint64, error)
	Close() error
}
```

`pkg/queue/store_queue.go`: `StoreQueue{store store.Store}` mapping Enqueue→`ZAdd`, Read→`Search` with `store.SearchCfg{Keys, Limit, From, To}` (mirror the worker loop's usage at `pkg/worker/worker.go:148-160`), Ack→`ZRem`, Requeue→`ZAdd`, Depth→`ZCard`. `NewStoreQueue(s store.Store) *StoreQueue`. `Close()` is a no-op (store lifecycle belongs to its owner).

`pkg/queue/config.go`: follows `docs/standards/CONFIG_GUIDE.md`:

```go
type Config struct {
	Provider string        `mapstructure:"provider"` // "inherit" (default) | "store"
	Store    store.Config  `mapstructure:"store"`    // used when provider=store
}
// SetDefaults: v.SetDefault(prefix+".provider", "inherit")
// Initialize(ctx, logger, mainStore store.Store) (Queue, error):
//   inherit → NewStoreQueue(mainStore)
//   store   → initialize its own store.Config, wrap it
```

- [ ] **Step 4:** `go test ./pkg/queue -v` → PASS. Commit `feat: queue interface with store-backed provider`.

### Task 2.2: Migrate consumers and producers

**Files:**
- Modify: `pkg/worker/worker.go`, `pkg/worker/config.go`, `pkg/jbsync/subscriber.go:83,214`, `pkg/overlay/p2p.go:106-109`, `pkg/overlay/event_bridge.go:99`, `pkg/overlay/services.go:298`, `pkg/overlay/sync.go`, `pkg/overlay/topic.go:48-53`, `pkg/gasp/sse_listener.go:169`, `pkg/gasp/queue_remote.go:44`, `pkg/gasp/beef_remote.go:49`, `pkg/bsv21/sync.go:163,246-247`, `pkg/bsv21/worker.go:46`, `pkg/node/wiring.go`, `pkg/node/config.go` (add `Queue queue.Config \`mapstructure:"queue"\``)

- [ ] **Step 1:** `worker.Config`: replace `Store store.Store` + `Key string` pair with `Queue queue.Queue` + `Key string`. Inside `Worker.Start`/`ProcessOnce`: `w.store.Search(...)` → `w.queue.Read(ctx, []byte(w.key), queue.ReadCfg{Limit: w.pageSize, To: &to})`; the retry `ZAdd` → `w.queue.Requeue(...)`; the completion `ZRem` → `w.queue.Ack(...)`.

- [ ] **Step 2:** Migrate each producer/reader listed in Files to take a `queue.Queue` (constructor signature change) and call `Enqueue`/`Read`/`Depth` instead of `ZAdd`/`ZRange`/`ZCard`. The `q:*` key values are unchanged.

- [ ] **Step 3:** In `pkg/node`: build the queue right after the main store (`queueSvc, err := c.Queue.Initialize(ctx, log, storeSvc.Store)`) and thread it through every changed constructor. Add `queue` section defaults to `SetDefaults`.

- [ ] **Step 4:** `go build ./... && go test ./...` → PASS. Verify no direct queue-key store ops remain: `grep -rn "ZAdd\|ZRem\|ZCard" pkg/jbsync pkg/overlay pkg/gasp pkg/bsv21 | grep -v _test` — remaining hits must not be `q:*` keys (txo/indexer tx logs are NOT queues and stay on store).

- [ ] **Step 5:** Milestone 1 parity check again (capabilities diff + default boot). Commit `refactor: queue producers/consumers behind queue.Queue`.

### Milestone 2 GATE

- [ ] `go test ./...` green; parity diff empty; `ONESAT_MODE=opns` still boots.
- [ ] A config smoke: set `queue.provider: store` with a distinct badger path; boot; verify `q:`-keyed data lands in the dedicated path (inspect with the admin data browser or a tiny Go snippet).
- [ ] Commit `chore: milestone 2 complete — queue abstraction`.

---

## Milestone 3 — Event-driven tx lifecycle; overlays drop txo

Outcome: the stale-rollback hole is closed; overlay modes run without txo/indexer/auditor; rejected/stale txs roll back overlay storage via events in both profiles.

### Task 3.1: PendingAuditor publishes rollback events

**Files:**
- Modify: `pkg/indexer/pending_auditor.go` (struct, constructor, `processUnconfirmed` stale branch ~line 324), `pkg/node/wiring.go` (auditor construction)
- Test: `pkg/indexer/pending_auditor_test.go`

- [ ] **Step 1: Failing test** — fake `pubsub.PubSub` capturing publishes; seed a pending member older than `rollbackAge` with no proof available (nil arcade client, beef storage returning `beef.ErrNotFound`); run `processUnconfirmed`; assert one publish on topic `"arc"` whose JSON decodes to `ArcEvent{TxID: <hex>, Status: "REJECTED"}` with `ExtraInfo` containing `"stale"`. Follow existing test style in the package.

- [ ] **Step 2:** Run it → FAIL.

- [ ] **Step 3:** Add `pubsub pubsub.PubSub` to `PendingAuditor` (constructor param; `pkg/node` passes the process pubsub). In the stale branch, after the successful `outputStore.Rollback` and log updates, publish:

```go
if a.pubsub != nil {
	evt, _ := json.Marshal(ArcEvent{TxID: txidHex, Status: "REJECTED", ExtraInfo: "stale: no proof before rollback threshold"})
	if err := a.pubsub.Publish(ctx, "arc", string(evt)); err != nil {
		a.logger.Error("failed to publish stale rollback", "txid", txidHex, "error", err)
	}
}
```

`StatusHandler.handleRejected` (the existing "arc" subscriber) then performs the overlay-storage rollback — no changes needed there. Verify `handleRejected` tolerates an event with no `RawTx` and no beef entry (it does: `status_handler.go:327-337`).

- [ ] **Step 4:** Tests pass; `go test ./...`; commit `fix: auditor publishes stale rollbacks so overlay storage rolls back too`.

### Task 3.2: Overlay modes run without txo

**Files:**
- Modify: `pkg/node/wiring.go`, `pkg/node/modes.go`

- [ ] **Step 1:** In `pkg/node` wiring, make txo OutputStore, indexer (IngestCtx/IngestSync), and PendingAuditor construction conditional on `c.runsService("index")`. The overlay `InitializeDeps.IngestTx` closure from Task 1.3 is passed only when index runs; otherwise pass `nil` (the adapter already nil-checks).

- [ ] **Step 2:** For processes running any overlay service but NOT index, wire the status feedback loop the opns-overlay way: arcade `EventBroker` + `indexer.StartArcBridge` (pubsub "arc") + a `StatusHandler` constructed with the process store, beef storage, overlay `engine.Storage`, topic index, and the running modules' lookup-service map — but with no txo-dependent pieces. Reference wiring: the monolith's StatusHandler setup (search `SetupStatusHandler` in `pkg/indexer/config.go:179` and its call in wiring) and `opns-overlay/cmd/server/main.go:159-170`.

- [ ] **Step 3:** Boot checks:

```bash
ONESAT_MODE=index go run ./cmd/server --data-dir /tmp/m3-index      # serves /txo, /owner, /tx, /sse
ONESAT_MODE=opns  go run ./cmd/server --data-dir /tmp/m3-opns       # serves /opns, no /txo routes
```

Assert via `/1sat/capabilities` on each. The opns process log must show the arc bridge and status handler starting, and must NOT construct OutputStore/indexer/auditor (add temporary `t.Log`-level verification by grepping logs, not code comments).

- [ ] **Step 4:** `go test ./...`; parity check (mode: all unchanged — index runs there, so full pipeline intact). Commit `feat: overlay modes run without txo; status loop via arc events`.

### Milestone 3 GATE

- [ ] Auditor publish test green; full suite green; parity diff empty.
- [ ] `mode: index` and `mode: opns` both boot with correct capability surfaces.
- [ ] Commit `chore: milestone 3 complete — event-driven lifecycle`.

---

## Milestone 4 — Remote providers

Outcome: spends can resolve against a remote index service; bsv21 no longer imports `indexer`/`owner` and can point owner-sync/balance at a remote index service.

### Task 4.1: spends HTTP provider

**Files:**
- Create: `pkg/spends/http.go`, `pkg/spends/http_test.go`
- Modify: `pkg/spends/config.go` (add `http` provider with `url`)

- [ ] **Step 1:** Read `pkg/txo/routes.go` handlers `GetSpend` (route `GET /:outpoint/spend`) and `GetSpends` (route `POST /spends`) and note their exact request/response encodings (path format, body format, hex vs binary).

- [ ] **Step 2: Failing test** — `httptest.Server` that mimics those handlers' encodings exactly; assert `HTTPSpendStorage.GetSpend` returns the txid for a known outpoint, `nil` for a 404/empty, and `GetSpends` maps a batch.

- [ ] **Step 3:** Implement `HTTPSpendStorage` (`NewHTTPSpendStorage(baseURL string, client *http.Client)`) satisfying `spends.BaseSpendStorage` (`pkg/spends/spends.go:13-19`). `PutSpend`/`DeleteSpend` return nil (read-only tier — writes belong to the index service). Add provider `http` to the spends chain config so a chain like `[lru, http, junglebus]` is expressible; base URL config key `spends.http.url`.

- [ ] **Step 4:** Tests pass; commit `feat: spends http provider`.

### Task 4.2: bsv21 owner/balance via injected interfaces

**Files:**
- Create: `pkg/bsv21/ownerclient.go`, `pkg/bsv21/ownerclient_test.go`
- Modify: `pkg/bsv21/sync.go:105-136`, `pkg/bsv21/worker.go:105-115`, `pkg/bsv21/manager.go` (fields/constructor), `pkg/bsv21/config.go` (add `owner` section), `pkg/node/wiring.go`

- [ ] **Step 1:** Define in `pkg/bsv21` (manager.go, next to the existing `OwnerSyncer`):

```go
// BalanceLookup returns unspent satoshis for an address.
type BalanceLookup func(ctx context.Context, address string) (int64, error)
```

`NewTokenManager` and `NewSyncServices` take `ownerSync OwnerSyncer, balance BalanceLookup` as parameters; delete the internal `indexer.NewIngestCtx` + `owner.NewOwnerSync` construction (`sync.go:109-120`). `GetTokenStatus` (`worker.go:105-115`) replaces the `outputStore.SearchBalance` block with `credits, err := m.balance(ctx, feeAddress)`. Remove the `outputStore` field if `grep -n "outputStore" pkg/bsv21/*.go` shows no remaining uses; remove `indexer`/`owner` imports.

- [ ] **Step 2: Failing test** for the HTTP client: `httptest.Server` mimicking `pkg/owner/routes.go` `OwnerSync` (`GET /sync`, query params — read the handler at `pkg/owner/routes.go:240` for exact params) and `OwnerBalance` (`GET /:owner/balance` — read handler at `:198` for the response JSON). Assert `HTTPOwnerClient.Sync` and `.Balance` behave.

- [ ] **Step 3:** Implement `pkg/bsv21/ownerclient.go`: `HTTPOwnerClient{baseURL, http.Client}` with `Sync(ctx, owner) error` and `Balance(ctx, addr) (int64, error)` matching those routes.

- [ ] **Step 4:** Config: `bsv21.owner.mode: embedded|remote` (default embedded), `bsv21.owner.url`. In `pkg/node` wiring: embedded → construct `owner.NewOwnerSync(...)` + wrap `outputStore.SearchBalance` into a `BalanceLookup` (this construction moves from bsv21 into node); remote → `HTTPOwnerClient` for both. Embedded requires `runsService("index")` — return a config error at startup if `bsv21.owner.mode=embedded` in a process without index.

- [ ] **Step 5:** `go test ./...`; verify `grep -rn "pkg/indexer\|pkg/owner" pkg/bsv21/` is empty; parity check; commit `refactor: bsv21 owner sync and balance behind injected interfaces`.

### Milestone 4 GATE

- [ ] Suite green; parity diff empty.
- [ ] `ONESAT_MODE=bsv21` with `bsv21.owner.mode=remote` + `url` pointing at a running `ONESAT_MODE=index` process boots and `GET /1sat/bsv21/...` status endpoint returns token status with credits fetched over HTTP (manual two-process smoke; capture the curl output in the commit message body).
- [ ] Commit `chore: milestone 4 complete — remote providers`.

---

## Milestone 5 — Gateway + per-service admin routes

Outcome: a `gateway` mode fronts a multi-process deployment with today's public surface; admin capabilities live on the services that own them.

### Task 5.1: Gateway service

**Files:**
- Create: `pkg/gateway/config.go`, `pkg/gateway/gateway.go`, `pkg/gateway/capabilities.go`, `pkg/gateway/gateway_test.go`

- [ ] **Step 1:** Config:

```go
type Config struct {
	Mode     string            `mapstructure:"mode"` // disabled|embedded
	Backends map[string]string `mapstructure:"backends"` // service name → base URL, e.g. index: "http://localhost:8081"
}
```

Path map derives from the same prefixes `RegisterRoutes` uses (see `moduleMounts` and the `prefixOr` defaults in `pkg/node/routes.go`): `/1sat/txo/*|/1sat/owner/*|/1sat/tx|/1sat/sse/*` → index; `/1sat/beef/*` → beef; `/1sat/bsv21/*` → bsv21; `/1sat/opns/*` → opns; `/1sat/bap/*` → bap; `/1sat/bsocial/*` → bsocial; `/1sat/market/*` → ordlock; `/1sat/ordfs/*` + `/content/*` → ordfs; `/1sat/bsvalias/*` + `/.well-known/bsvalias` → paymail; `/.well-known/auth` → index.

- [ ] **Step 2: Failing test** — two `httptest.Server` backends (fake index returning a marker on `/1sat/txo/ping`, fake opns with `/1sat/capabilities` returning `["opns"]`); gateway configured with both; assert (a) proxying reaches the right backend, (b) `GET /1sat/capabilities` on the gateway returns the merged, sorted union of backend capability lists.

- [ ] **Step 3:** Implement with `github.com/gofiber/fiber/v2/middleware/proxy` (`proxy.Do(c, url)`) — one route registration per prefix per configured backend; skip prefixes whose backend is not configured. `capabilities.go`: fan out `GET {backend}/1sat/capabilities`, merge JSON arrays, dedupe, sort; 5s timeout per backend; omit unreachable backends and log them.

- [ ] **Step 4:** Register in `pkg/node`: when `runsService("gateway")`, mount gateway routes on the fiber app INSTEAD of local service routes (gateway is expected to run alone in its process; return a startup error if gateway is combined with other modes in one list). Tests pass; commit `feat: gateway mode`.

### Task 5.2: Per-service admin routes

**Files:**
- Modify: `admin/data_routes.go` → move handlers into `pkg/txo/admin_routes.go` (new); `admin/routes.go` (remove data-route registration, keep config/setup/auth/log routes); `pkg/opns/routes.go` (add crawl trigger); `pkg/bsv21/routes.go` (add whitelist/blacklist/token-status admin endpoints); `pkg/node/routes.go` (mount moved surfaces at their EXISTING public paths)

- [ ] **Step 1:** Inventory first: `grep -rn "fetch\|axios\|/admin" admin/ui/src --include=*.ts* -l` and list every admin API path the UI calls. Record the list in the task's commit message body. Requirement: every path keeps working in `mode: all`.

- [ ] **Step 2:** Move the store data-browser handlers (`admin/data_routes.go:127-840`) into `pkg/txo/admin_routes.go` as an `AdminRoutes` type registered by the index service, guarded by the same `auth.AdminGuard` middleware (see current guard wiring in `pkg/node/routes.go` admin mount). Mount at the identical public path prefix the UI already calls so the UI needs no change in `mode: all`.

- [ ] **Step 3:** Add admin-guarded endpoints on owning services, matching what `admin/` currently does in-process: opns crawl trigger (the `OpnsCrawl` hook passed into admin — see `admin.InitializeDeps`), bsv21 whitelist/blacklist CRUD (currently `admin/routes.go:221,320` writing config-store keys — the endpoints move to bsv21, still writing its config store) and token status (wraps `TokenManager.GetTokenStatus`).

- [ ] **Step 4:** `admin/` package keeps: setup wizard, config CRUD, auth admin, log viewer, restart — the control-plane surface. It must no longer receive `Store`, engines map, `BSV21Sync`, or `OpnsCrawl` in `InitializeDeps`; delete those fields and their uses (the UI now reaches those features through the per-service endpoints at unchanged paths).

- [ ] **Step 5:** `go test ./...`; boot `mode: all`; exercise the admin UI's data browser, whitelist page, and crawl trigger manually against the running server. Parity diff. Commit `refactor: admin capabilities move to owning services`.

### Milestone 5 GATE (final)

- [ ] Full suite green; `mode: all` parity diff empty; admin UI functional in `mode: all`.
- [ ] Three-process smoke: `index` (port 8081), `opns` (port 8082), `gateway` (port 8080, backends configured) — `curl http://localhost:8080/1sat/opns/...` and `/1sat/txo/...` route correctly; `/1sat/capabilities` merged.
- [ ] Update `docs/plans/STATUS.md` (this plan → Complete or note stopping point) and `docs/plans/microservice-split.md` gap-list checkboxes for delivered items.
- [ ] Final commit `chore: milestone 5 complete — gateway + per-service admin`.

---

## Out of scope (do not build)

- Configurator / compose generation (gap 8).
- opns-overlay repo changes (gap 7 — separate repo).
- bsv21 metering split (parked).
- Any admin UI redesign beyond keeping existing pages working.

## Requirements checklist (measurable, morning-after review)

- [ ] R1: `mode: all` capabilities/docs identical to master baseline; `go test ./...` green.
- [ ] R2: `cmd/server` contains no wiring (only flag parsing, logger, signals, fiber app, node calls).
- [ ] R3: explicit mode lists boot subset processes with correct capability surfaces (`index`, `opns`, `bsv21` verified).
- [ ] R4: all `q:*` traffic flows through `queue.Queue`; a dedicated queue backend is configurable.
- [ ] R5: stale unconfirmed txs produce an `arc` REJECTED event; overlay storage rolls back (unit-tested).
- [ ] R6: overlay-mode processes construct no OutputStore/indexer/auditor.
- [ ] R7: `pkg/bsv21` imports neither `pkg/indexer` nor `pkg/owner`; remote owner mode works cross-process.
- [ ] R8: spends chain supports an HTTP tier.
- [ ] R9: gateway proxies the public surface and serves merged capabilities.
- [ ] R10: admin UI works unchanged in `mode: all`; admin package holds no cross-service handles.
