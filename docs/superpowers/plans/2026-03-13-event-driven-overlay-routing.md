# Event-Driven Overlay Routing Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route indexed transactions to overlay processing queues via PubSub events, so overlays process transactions regardless of entry path (Arcade, owner sync, GASP, JungleBus).

**Architecture:** After `SaveTransaction` stores outputs/spends to Badger, it publishes txid-level routing events to PubSub. Each overlay module's EventBridge subscribes to relevant patterns and enqueues the txid into the module's processing queue. The existing OverlaySync worker picks it up.

**Tech Stack:** Go, in-memory PubSub (ChannelPubSub), Badger sorted sets for queues, go-templates for script parsing.

**Spec:** `docs/plans/event-driven-overlay-routing.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `pkg/pubsub/channels.go` | Modify | Add glob pattern matching to Subscribe/Publish |
| `pkg/pubsub/channels_test.go` | Create | Tests for pattern matching |
| `pkg/parse/ordlock.go` | Modify | Change event from `"ordlock:list"` to `"ordlock"` |
| `pkg/parse/bitcom.go` | Modify | Add BAP events, simplify MAP events |
| `pkg/parse/opns.go` | Create | New OPNS mine parser |
| `pkg/parse/parse.go` | Modify | Register OPNS parser in Parsers map and DefaultTags |
| `pkg/parse/parse_test.go` | Create | Tests for new/modified parsers |
| `pkg/overlay/event_bridge.go` | Create | EventBridge: PubSub → queue routing |
| `pkg/overlay/event_bridge_test.go` | Create | Tests for EventBridge |
| `pkg/txo/output_store.go` | Modify | Add txid-level publishing in SaveTransaction, remove outpoint publishing |
| `pkg/opns/config.go` | Modify | Add `Sync *overlay.OverlaySync` field to OPNS Services struct |
| `config.example.yaml` | Modify | Add routing-relevant parser tags (opns, bitcom, map, bap) |
| `cmd/server/config.go` | Modify | Wire EventBridges, decouple worker start from JungleBus |

**BSV21 note:** BSV21 is excluded from EventBridge wiring. BSV21 workers consume binary outpoint bytes, not txid hex strings. BSV21 already has its own sync pipeline that handles queue population. EventBridge serves BAP, BSocial, OrdLock, and OPNS only.

---

## Chunk 1: PubSub Pattern Matching

### Task 1: Add glob pattern matching to ChannelPubSub

**Files:**
- Modify: `pkg/pubsub/channels.go`
- Create: `pkg/pubsub/channels_test.go`

- [ ] **Step 1: Write failing tests for pattern matching**

Create `pkg/pubsub/channels_test.go`:

```go
package pubsub

import (
	"context"
	"testing"
	"time"
)

func TestChannelPubSub_ExactMatch(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, []string{"ordlock"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(ctx, "ordlock", "abc123"); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Member != "abc123" || ev.Topic != "ordlock" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestChannelPubSub_GlobPattern(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, []string{"bsv21:*"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(ctx, "bsv21:abc123_0", "txid1"); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Member != "txid1" || ev.Topic != "bsv21:abc123_0" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for glob match")
	}
}

func TestChannelPubSub_GlobNoMatch(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, []string{"bsv21:*"})
	if err != nil {
		t.Fatal(err)
	}

	// Publish to a different prefix — should NOT match
	if err := ps.Publish(ctx, "bap:ID", "txid2"); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		t.Fatalf("should not have received event: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event
	}
}

func TestChannelPubSub_MultiplePatterns(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe to both exact and glob
	ch, err := ps.Subscribe(ctx, []string{"ordlock", "spend:ordlock"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(ctx, "ordlock", "txid1"); err != nil {
		t.Fatal(err)
	}
	if err := ps.Publish(ctx, "spend:ordlock", "txid2"); err != nil {
		t.Fatal(err)
	}

	received := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			received[ev.Member] = true
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
	if !received["txid1"] || !received["txid2"] {
		t.Fatalf("missing events: %v", received)
	}
}

func TestChannelPubSub_UnsubscribeOnCancel(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := ps.Subscribe(ctx, []string{"bap:*"})
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	// Give cleanup goroutine time to run
	time.Sleep(50 * time.Millisecond)

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Fatal("channel should be closed after cancel")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/pubsub/ -v -run TestChannelPubSub`

Expected: `TestChannelPubSub_GlobPattern` fails (no glob support yet). Other tests may pass since they use exact match.

- [ ] **Step 3: Implement pattern matching in ChannelPubSub**

Modify `pkg/pubsub/channels.go`. Add a `patternSubs` field that stores subscriptions with glob patterns. On `Publish`, after the exact match lookup, iterate pattern subscriptions and check if the topic matches.

The key changes:
1. Add `patternSubs` slice field to `ChannelPubSub` (mutex-protected, small fixed set)
2. In `Subscribe`, detect `*` in topic strings → store in `patternSubs` instead of exact `subscribers` map
3. In `Publish`, after exact lookup, iterate `patternSubs` and check prefix match
4. In `unsubscribeSubscription`, remove from `patternSubs` if it was a pattern subscription

Pattern matching logic: a topic string containing `*` is a glob. `"bsv21:*"` matches any topic starting with `"bsv21:"`. Convert by stripping the trailing `*` and doing a `strings.HasPrefix` check. This is sufficient for our use case (all patterns are `prefix:*` form).

```go
// Add to ChannelPubSub struct:
type patternSubscription struct {
	prefix string // e.g., "bsv21:" from pattern "bsv21:*"
	sub    *channelSubscription
}

type ChannelPubSub struct {
	subscribers sync.Map // topic -> []*channelSubscription (exact match)
	patternMu   sync.RWMutex
	patternSubs []*patternSubscription
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *slog.Logger
}
```

In `Subscribe`, split topics into exact and pattern:

```go
func (cp *ChannelPubSub) Subscribe(ctx context.Context, topics []string) (<-chan Event, error) {
	eventChan := make(chan Event, eventChannelBuffer)

	sub := &channelSubscription{
		ctx:     ctx,
		channel: eventChan,
		topics:  topics,
	}

	for _, topic := range topics {
		if strings.HasSuffix(topic, "*") {
			prefix := topic[:len(topic)-1] // "bsv21:*" → "bsv21:"
			cp.patternMu.Lock()
			cp.patternSubs = append(cp.patternSubs, &patternSubscription{
				prefix: prefix,
				sub:    sub,
			})
			cp.patternMu.Unlock()
		} else {
			var subs []*channelSubscription
			if existing, ok := cp.subscribers.Load(topic); ok {
				subs = existing.([]*channelSubscription)
			}
			subs = append(subs, sub)
			cp.subscribers.Store(topic, subs)
		}
	}

	go func() {
		<-ctx.Done()
		cp.unsubscribeSubscription(sub)
		close(eventChan)
	}()

	return eventChan, nil
}
```

In `Publish`, add pattern matching after exact match:

```go
func (cp *ChannelPubSub) Publish(ctx context.Context, topic string, data string, score ...float64) error {
	var eventScore float64
	if len(score) > 0 {
		eventScore = score[0]
	}

	event := Event{
		Topic:  topic,
		Member: data,
		Score:  eventScore,
		Source: "channels",
	}

	// Exact match subscribers
	if subs, ok := cp.subscribers.Load(topic); ok {
		subscriptions := subs.([]*channelSubscription)
		for _, sub := range subscriptions {
			select {
			case sub.channel <- event:
			case <-ctx.Done():
				return ctx.Err()
			default:
				cp.logger.Warn("skipping full channel", "topic", topic)
			}
		}
	}

	// Pattern match subscribers
	cp.patternMu.RLock()
	patterns := cp.patternSubs
	cp.patternMu.RUnlock()

	for _, ps := range patterns {
		if strings.HasPrefix(topic, ps.prefix) {
			select {
			case ps.sub.channel <- event:
			case <-ctx.Done():
				return ctx.Err()
			default:
				cp.logger.Warn("skipping full pattern channel", "topic", topic, "pattern", ps.prefix+"*")
			}
		}
	}

	return nil
}
```

Update `unsubscribeSubscription` to also clean pattern subs:

```go
func (cp *ChannelPubSub) unsubscribeSubscription(targetSub *channelSubscription) {
	// Remove from exact match subscribers
	for _, topic := range targetSub.topics {
		if !strings.HasSuffix(topic, "*") {
			if subs, ok := cp.subscribers.Load(topic); ok {
				subscriptions := subs.([]*channelSubscription)
				var newSubs []*channelSubscription
				for _, sub := range subscriptions {
					if sub != targetSub {
						newSubs = append(newSubs, sub)
					}
				}
				if len(newSubs) == 0 {
					cp.subscribers.Delete(topic)
				} else {
					cp.subscribers.Store(topic, newSubs)
				}
			}
		}
	}

	// Remove from pattern subscribers
	cp.patternMu.Lock()
	var remaining []*patternSubscription
	for _, ps := range cp.patternSubs {
		if ps.sub != targetSub {
			remaining = append(remaining, ps)
		}
	}
	cp.patternSubs = remaining
	cp.patternMu.Unlock()
}
```

Add `"strings"` to imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/pubsub/ -v -run TestChannelPubSub`

Expected: All 5 tests PASS.

- [ ] **Step 5: Run full test suite**

Run: `go vet ./pkg/pubsub/ && go test ./pkg/pubsub/`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/pubsub/channels.go pkg/pubsub/channels_test.go
git commit -m "Add glob pattern matching to ChannelPubSub

Subscribe topics ending with * are treated as prefix patterns.
Publish checks exact subscribers first, then iterates pattern
subscribers (small fixed set). Maps to Redis PSUBSCRIBE."
```

---

## Chunk 2: Parser Changes

### Task 2: Simplify OrdLock parser event

**Files:**
- Modify: `pkg/parse/ordlock.go:28`

- [ ] **Step 1: Change event from `"ordlock:list"` to `"ordlock"`**

In `pkg/parse/ordlock.go`, line 28, change:
```go
Events: []string{"ordlock:list"},
```
to:
```go
Events: []string{"ordlock"},
```

- [ ] **Step 2: Verify build**

Run: `go vet ./pkg/parse/`

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add pkg/parse/ordlock.go
git commit -m "Simplify ordlock parser event to plain 'ordlock'"
```

### Task 3: Simplify MAP parser events and add BAP events

**Files:**
- Modify: `pkg/parse/bitcom.go:74-79` (ParseMAP) and `pkg/parse/bitcom.go:121-129` (ParseBAP)

- [ ] **Step 1: Simplify ParseMAP — remove `map:app:` event**

In `pkg/parse/bitcom.go`, in `ParseMAP`, remove the `map:app` event emission. Lines 77-79:
```go
			if app, ok := m.Data["app"]; ok {
				result.Events = append(result.Events, "map:app:"+app)
			}
```
Delete those 3 lines.

- [ ] **Step 2: Add BAP events to ParseBAP**

In `pkg/parse/bitcom.go`, `ParseBAP` function (line 115-130). Currently returns no events. Change the return to include the BAP type as an event:

Replace:
```go
	return &ParseResult{
		Tag:  TagBAP,
		Data: bap,
	}, nil
```
With:
```go
	return &ParseResult{
		Tag:    TagBAP,
		Data:   bap,
		Events: []string{"bap:" + string(bap.Type)},
	}, nil
```

`bap.Type` is a `bitcom.AttestationType` (string type) with values `"ID"`, `"ATTEST"`, `"REVOKE"`.

- [ ] **Step 3: Verify build**

Run: `go vet ./pkg/parse/`

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/parse/bitcom.go
git commit -m "Simplify MAP events, add BAP routing events

MAP now only emits map:type:{type}, drops map:app:{app}.
BAP now emits bap:{operation} (bap:ID, bap:ATTEST, bap:REVOKE)."
```

### Task 4: Create OPNS mine parser

**Files:**
- Create: `pkg/parse/opns.go`
- Modify: `pkg/parse/parse.go:37-71`

- [ ] **Step 1: Create ParseOPNS parser**

Create `pkg/parse/opns.go`:

```go
package parse

import (
	"github.com/bitcoin-sv/go-templates/template/opns"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagOPNS = "opns"

// ParseOPNS detects OPNS mine outputs via opns.Decode().
func ParseOPNS(ctx *ParseContext) (*ParseResult, error) {
	scr := script.NewFromBytes(ctx.LockingScript)
	if opns.Decode(scr) == nil {
		return nil, nil
	}

	return &ParseResult{
		Tag:    TagOPNS,
		Events: []string{"opns:mine"},
	}, nil
}
```

- [ ] **Step 2: Register in Parsers map and DefaultTags**

In `pkg/parse/parse.go`, add to `Parsers` map (after `TagShrug` entry, line 44):
```go
	TagOPNS:        ParseOPNS,
```

Add to `DefaultTags` slice (before `TagBitcom`, around line 64 — after `TagCosign`):
```go
	TagOPNS,        // OpNS mine outputs
```

- [ ] **Step 3: Verify build**

Run: `go vet ./pkg/parse/`

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/parse/opns.go pkg/parse/parse.go
git commit -m "Add OPNS mine parser

Detects OpNS mine outputs via opns.Decode() and emits 'opns:mine'
event for overlay routing."
```

### Task 4b: Update default parser tags in config

**Files:**
- Modify: `config.example.yaml:87-94`

The indexer config has an explicit `tags` list that controls which parsers run. Currently it only has: `1sat`, `p2pkh`, `lock`, `inscription`, `bsv21`, `ordlock`, `cosign`. The new routing events require additional parsers to be active.

- [ ] **Step 1: Add routing-relevant tags to config.example.yaml**

In `config.example.yaml`, update the `tags` list under `indexer:` to add the parsers needed for event-driven routing:

```yaml
  tags:
    - 1sat         # 1Sat ordinals
    - p2pkh        # Pay-to-public-key-hash
    - lock         # Lock scripts
    - inscription  # Ordinal inscriptions
    - bsv21        # BSV21 fungible tokens
    - ordlock      # Ordinal lock scripts
    - cosign       # Co-signing scripts
    - opns         # OpNS mine outputs
    - bitcom       # Base bitcom parser (required by map, bap)
    - map          # MAP protocol (emits map:type events for BSocial routing)
    - bap          # BAP identity (emits bap:{operation} events)
    - origin       # Origin resolution for transferred 1-sat outputs
```

Note: `bitcom` must come before `map` and `bap` in this list since those parsers depend on bitcom output. The `origin` parser is also added since it was previously missing and is useful for origin resolution.

- [ ] **Step 2: Commit**

```bash
git add config.example.yaml
git commit -m "Add routing-relevant parser tags to example config

Add opns, bitcom, map, bap, origin to the indexer tags list.
These parsers emit events needed for overlay routing."
```

---

## Chunk 3: EventBridge

### Task 5: Create EventBridge

**Files:**
- Create: `pkg/overlay/event_bridge.go`
- Create: `pkg/overlay/event_bridge_test.go`

- [ ] **Step 1: Write failing test for EventBridge**

Create `pkg/overlay/event_bridge_test.go`:

```go
package overlay

import (
	"context"
	"testing"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
)

// mockStore implements store.Store for testing ZAdd calls
type mockStore struct {
	store.Store
	added map[string][]store.ScoredMember
}

func newMockStore() *mockStore {
	return &mockStore{added: make(map[string][]store.ScoredMember)}
}

func (m *mockStore) ZAdd(ctx context.Context, key []byte, members ...store.ScoredMember) error {
	k := string(key)
	m.added[k] = append(m.added[k], members...)
	return nil
}

func TestEventBridge_RoutesToQueue(t *testing.T) {
	ps := pubsub.NewChannelPubSub(nil)
	defer ps.Close()
	ms := newMockStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := NewEventBridge(&EventBridgeConfig{
		PubSub:   ps,
		Store:    ms,
		Patterns: []string{"ordlock", "spend:ordlock"},
		QueueFunc: func(ev pubsub.Event) string {
			return "q:ordlock"
		},
	})

	if err := bridge.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Publish an event
	if err := ps.Publish(ctx, "ordlock", "abcd1234"); err != nil {
		t.Fatal(err)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	members, ok := ms.added["q:ordlock"]
	if !ok || len(members) == 0 {
		t.Fatal("expected txid to be enqueued")
	}
	if string(members[0].Member) != "abcd1234" {
		t.Fatalf("unexpected member: %s", string(members[0].Member))
	}
	if members[0].Score <= 0 {
		t.Fatal("expected positive timestamp score")
	}
}

func TestEventBridge_SkipsOnEmptyQueueKey(t *testing.T) {
	ps := pubsub.NewChannelPubSub(nil)
	defer ps.Close()
	ms := newMockStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := NewEventBridge(&EventBridgeConfig{
		PubSub:   ps,
		Store:    ms,
		Patterns: []string{"map:type:*"},
		QueueFunc: func(ev pubsub.Event) string {
			return "" // skip
		},
	})

	if err := bridge.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(ctx, "map:type:photo", "txid1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	if len(ms.added) > 0 {
		t.Fatal("should not have enqueued anything")
	}
}

func TestEventBridge_DynamicQueueKey(t *testing.T) {
	ps := pubsub.NewChannelPubSub(nil)
	defer ps.Close()
	ms := newMockStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := NewEventBridge(&EventBridgeConfig{
		PubSub:   ps,
		Store:    ms,
		Patterns: []string{"bsv21:*"},
		QueueFunc: func(ev pubsub.Event) string {
			// Extract tokenId from "bsv21:{tokenId}"
			if len(ev.Topic) > 6 {
				return "q:tm_" + ev.Topic[6:]
			}
			return ""
		},
	})

	if err := bridge.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(ctx, "bsv21:abc123_0", "txid1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	members, ok := ms.added["q:tm_abc123_0"]
	if !ok || len(members) == 0 {
		t.Fatal("expected txid to be enqueued in token queue")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/overlay/ -v -run TestEventBridge`

Expected: FAIL (EventBridge doesn't exist yet)

- [ ] **Step 3: Implement EventBridge**

Create `pkg/overlay/event_bridge.go`:

```go
package overlay

import (
	"context"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
)

// EventBridgeConfig configures an EventBridge instance.
type EventBridgeConfig struct {
	PubSub    pubsub.PubSub
	Store     store.Store
	Patterns  []string
	QueueFunc func(pubsub.Event) string // returns queue key, empty to skip
	Logger    *slog.Logger
}

// EventBridge subscribes to PubSub patterns and enqueues txids into store queues.
type EventBridge struct {
	config *EventBridgeConfig
	logger *slog.Logger
}

// NewEventBridge creates a new EventBridge.
func NewEventBridge(cfg *EventBridgeConfig) *EventBridge {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EventBridge{
		config: cfg,
		logger: logger.With("component", "event-bridge"),
	}
}

// Start subscribes to patterns and begins routing events to queues.
// Returns immediately after subscription; processing runs in a goroutine.
func (eb *EventBridge) Start(ctx context.Context) error {
	ch, err := eb.config.PubSub.Subscribe(ctx, eb.config.Patterns)
	if err != nil {
		return err
	}

	go eb.run(ctx, ch)
	return nil
}

func (eb *EventBridge) run(ctx context.Context, ch <-chan pubsub.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			queueKey := eb.config.QueueFunc(ev)
			if queueKey == "" {
				continue
			}
			if err := eb.config.Store.ZAdd(ctx, []byte(queueKey), store.ScoredMember{
				Member: []byte(ev.Member),
				Score:  float64(time.Now().UnixMicro()),
			}); err != nil {
				eb.logger.Error("failed to enqueue txid",
					"queue", queueKey, "txid", ev.Member, "error", err)
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/overlay/ -v -run TestEventBridge`

Expected: All 3 tests PASS.

- [ ] **Step 5: Run full overlay package tests**

Run: `go vet ./pkg/overlay/ && go test ./pkg/overlay/`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/overlay/event_bridge.go pkg/overlay/event_bridge_test.go
git commit -m "Add EventBridge for PubSub-to-queue routing

Subscribes to PubSub patterns, calls QueueFunc to determine target
queue, ZAdds txid with microsecond timestamp score."
```

---

## Chunk 4: SaveTransaction Publishing and Outpoint Publish Removal

### Task 6: Add txid-level event publishing in SaveTransaction

**Files:**
- Modify: `pkg/txo/output_store.go:134-140` (remove outpoint publish in SaveOutput)
- Modify: `pkg/txo/output_store.go:185-191` (remove outpoint publish in SaveEvents)
- Modify: `pkg/txo/output_store.go:226-265` (add txid-level publish in SaveTransaction)

- [ ] **Step 1: Remove outpoint-level publishing from SaveOutput**

In `pkg/txo/output_store.go`, delete lines 134-140 in `SaveOutput`:

```go
	// Publish events
	if s.PubSub != nil {
		opStr := op.String()
		for _, event := range events {
			s.PubSub.Publish(ctx, event, opStr)
		}
	}
```

- [ ] **Step 2: Remove outpoint-level publishing from SaveEvents**

In `pkg/txo/output_store.go`, delete lines 185-191 in `SaveEvents`:

```go
	// Publish events
	if s.PubSub != nil {
		opStr := op.String()
		for _, event := range events {
			s.PubSub.Publish(ctx, event, opStr)
		}
	}
```

- [ ] **Step 3: Add txid-level publishing in SaveTransaction**

In `pkg/txo/output_store.go`, in `SaveTransaction`, after the spends loop and before the `tx:pending` log (between the spend loop ending and the ZAdd to PendingTxLog), add:

```go
	// Publish txid-level routing events
	if s.PubSub != nil {
		routingEvents := make(map[string]struct{})

		// Collect output events
		for _, output := range outputs {
			if output == nil {
				continue
			}
			for _, event := range output.Events {
				routingEvents[event] = struct{}{}
			}
		}

		// Collect spend events (prefix with "spend:")
		for _, spend := range spends {
			if spend == nil {
				continue
			}
			for _, event := range spend.Events {
				routingEvents["spend:"+event] = struct{}{}
			}
		}

		// Publish each unique event once with the txid
		for event := range routingEvents {
			s.PubSub.Publish(ctx, event, txidHex)
		}
	}
```

Note: `routingEvents` includes ALL events from outputs (including `txid:`, `own:`, `p2pkh:`, etc.). Only overlay modules that subscribe to relevant patterns will act on them. Non-routing events like `txid:` and `own:` pass through harmlessly since no EventBridge subscribes to them.

- [ ] **Step 4: Verify build**

Run: `go vet ./pkg/txo/`

Expected: PASS

- [ ] **Step 5: Run tests**

Run: `go test ./pkg/txo/`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/txo/output_store.go
git commit -m "Switch PubSub from outpoint-level to txid-level events

Remove per-outpoint Publish in SaveOutput/SaveEvents (nothing
subscribes to them). Add txid-level routing event publishing in
SaveTransaction after all outputs and spends are stored."
```

---

## Chunk 5: Module Initialization Wiring

### Task 7: Wire EventBridges and decouple workers from JungleBus

**Files:**
- Modify: `cmd/server/config.go:515-635` (module init sections)
- Modify: `cmd/server/config.go:1408-1462` (StartSubscribers)

This is the integration task. For each overlay module, we need to:

1. Always create the OverlaySync worker (not gated on `sync.enabled`)
2. Create an EventBridge that subscribes to the module's patterns
3. Keep JungleBus subscription as optional (only if `sync.enabled` and subscription ID set)

- [ ] **Step 1: Refactor BAP initialization (config.go ~line 538)**

Change:
```go
		if c.BAP.Sync != nil && c.BAP.Sync.Enabled && svc.Beef != nil {
			svc.BAP.Sync = overlay.NewOverlaySync(c.BAP.Sync, "tm_bap", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
		}
```

To always create the sync worker when beef is available, with a default config if none provided:
```go
		if svc.Beef != nil {
			syncCfg := c.BAP.Sync
			if syncCfg == nil {
				syncCfg = &overlay.OverlaySyncConfig{}
			}
			if syncCfg.QueueName == "" {
				syncCfg.QueueName = "bap"
			}
			svc.BAP.Sync = overlay.NewOverlaySync(syncCfg, "tm_bap", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
		}
```

- [ ] **Step 2: Refactor BSocial initialization (config.go ~line 568)**

Same pattern as BAP. Change:
```go
		if c.BSocial.Sync != nil && c.BSocial.Sync.Enabled && svc.Beef != nil {
			svc.BSocial.Sync = overlay.NewOverlaySync(c.BSocial.Sync, "tm_bsocial", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
		}
```

To:
```go
		if svc.Beef != nil {
			syncCfg := c.BSocial.Sync
			if syncCfg == nil {
				syncCfg = &overlay.OverlaySyncConfig{}
			}
			if syncCfg.QueueName == "" {
				syncCfg.QueueName = "bsocial"
			}
			svc.BSocial.Sync = overlay.NewOverlaySync(syncCfg, "tm_bsocial", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
		}
```

- [ ] **Step 3: Refactor OrdLock initialization (config.go ~line 630)**

Change:
```go
		if c.OrdLock.Sync != nil && c.OrdLock.Sync.Enabled && svc.Beef != nil {
			svc.OrdLock.Sync = overlay.NewOverlaySync(c.OrdLock.Sync, ordlockpkg.TopicName, svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
		}
```

To:
```go
		if svc.Beef != nil {
			syncCfg := c.OrdLock.Sync
			if syncCfg == nil {
				syncCfg = &overlay.OverlaySyncConfig{}
			}
			if syncCfg.QueueName == "" {
				syncCfg.QueueName = "ordlock"
			}
			svc.OrdLock.Sync = overlay.NewOverlaySync(syncCfg, ordlockpkg.TopicName, svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
		}
```

- [ ] **Step 4: Add OPNS OverlaySync worker (config.go ~line 601)**

After the OPNS crawl setup (after `}` closing the crawl block, before the closing `}` of the OPNS init block), add:

```go
		if svc.Beef != nil {
			opnsSyncCfg := &overlay.OverlaySyncConfig{
				QueueName:           "opns",
				ResolveDependencies: true,
			}
			svc.OPNS.Sync = overlay.NewOverlaySync(opnsSyncCfg, "tm_opns", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
		}
```

First, add a `Sync` field to the OPNS services struct in `pkg/opns/config.go`:

```go
Sync *overlay.OverlaySync
```

Add the import for `overlay "github.com/b-open-io/1sat-stack/pkg/overlay"` to the file.

- [ ] **Step 5: Create EventBridges in StartSubscribers**

In `StartSubscribers` (config.go ~line 1408), add EventBridge creation for each module. Add after the JungleBus subscriber starts, before the overlay sync worker starts. The cleanest approach is to add a new section after line 1418.

Add import for the overlay package if not already imported.

```go
	// Start EventBridges (PubSub → overlay queues)
	if svc.PubSub != nil {
		if svc.OrdLock != nil && svc.OrdLock.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"ordlock", "spend:ordlock"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("ordlock"))
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OrdLock event bridge", "error", err)
			}
		}
		if svc.BAP != nil && svc.BAP.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"bap:*"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("bap"))
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BAP event bridge", "error", err)
			}
		}
		if svc.BSocial != nil && svc.BSocial.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"map:type:*"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("bsocial"))
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BSocial event bridge", "error", err)
			}
		}
		if svc.OPNS != nil && svc.OPNS.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"opns:mine"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("opns"))
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OPNS event bridge", "error", err)
			}
		}
		// BSV21 excluded — its workers consume binary outpoint bytes, not txid hex.
		// BSV21 has its own sync pipeline that handles queue population.
	}
```

- [ ] **Step 6: Update StartSubscribers to always start sync workers**

Change the sync worker start conditions from `svc.XXX.Sync != nil` (which was only set when sync.enabled) to always start when the sync object exists. Since we now always create the sync object in Step 1-4, the existing conditions (`svc.BAP.Sync != nil`) already work.

Also add OPNS sync worker start. After the OrdLock sync start block (~line 1453), add:

```go
	if svc.OPNS != nil && svc.OPNS.Sync != nil {
		go func() {
			if err := svc.OPNS.Sync.Start(ctx); err != nil {
				logger.Error("OPNS sync error", "error", err)
			}
		}()
		logger.Info("started OPNS overlay sync")
	}
```

The existing OPNS crawl start block remains as-is (it's a separate concern).

- [ ] **Step 7: Add JungleBus subscriber creation gated on sync.enabled**

Currently, JungleBus subscribers are created during module init and added to `svc.JBSubscribers`. Check if this is still gated correctly. The JungleBus subscriber is created separately from the OverlaySync worker — the subscriber fills the queue, the worker drains it. The subscriber should only be created when `sync.enabled` AND `subscription_id` is set.

Verify this is already the case by checking how JBSubscribers are populated. If the existing logic already gates on `sync.enabled`, no change needed. If it was previously tied to the OverlaySync creation, it needs to be separated.

Look for patterns like:
```go
if c.BAP.Sync != nil && c.BAP.Sync.Enabled {
    // create JB subscriber
    svc.JBSubscribers = append(...)
}
```

This should remain gated on `Enabled`. The OverlaySync worker (now always created) is separate.

- [ ] **Step 8: Verify build**

Run: `go vet ./cmd/server/`

Expected: PASS. If there are import issues (pubsub, txo, overlay), fix them.

- [ ] **Step 9: Verify full build**

Run: `go build ./cmd/server && rm -f server`

Expected: Build succeeds.

- [ ] **Step 10: Commit**

```bash
git add cmd/server/config.go pkg/opns/
git commit -m "Wire EventBridges and decouple workers from JungleBus

Each overlay module now always starts its sync worker when enabled.
EventBridges route PubSub events to the appropriate queue.
JungleBus subscription remains optional (sync.enabled).
OPNS gets a new OverlaySync worker (previously only had crawl)."
```

---

## Chunk 6: Verification

### Task 8: End-to-end verification

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`

Expected: All tests pass. If any fail, investigate and fix.

- [ ] **Step 2: Run vet and build**

Run: `go vet ./... && go build ./cmd/server && rm -f server`

Expected: Clean vet, successful build.

- [ ] **Step 3: Update STATUS.md**

Update `docs/plans/STATUS.md` to reflect the event-driven overlay routing plan status change from "Not Started" to "In Progress" or "Complete" depending on deployment status.

Update `docs/plans/event-driven-overlay-routing.md` status line from `**Not Started**` to `**In Progress**`.

- [ ] **Step 4: Final commit**

```bash
git add docs/plans/
git commit -m "Update plan status for event-driven overlay routing"
```
