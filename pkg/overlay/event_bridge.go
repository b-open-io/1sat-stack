package overlay

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type EventBridgeConfig struct {
	PubSub    pubsub.PubSub
	Store     store.Store
	Patterns  []string
	QueueFunc func(pubsub.Event) string
	Logger    *slog.Logger

	// Direct overlay submission (optional). When SubmitBuffer > 0,
	// the bridge attempts engine.Submit immediately on each event
	// with a buffered channel for backpressure.
	Engine        *engine.Engine
	BeefStorage   *beef.Storage
	SubmitBuffer  int // channel size; 0 = queue-only (disabled)
	SubmitWorkers int // goroutines draining submit channel; default 1
}

type EventBridge struct {
	config    *EventBridgeConfig
	logger    *slog.Logger
	submitCh  chan submitItem
	submitted sync.Map // chainhash.Hash → struct{} dedup
}

type submitItem struct {
	txid  *chainhash.Hash
	topic string
}

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

func (eb *EventBridge) Start(ctx context.Context) error {
	ch, err := eb.config.PubSub.Subscribe(ctx, eb.config.Patterns)
	if err != nil {
		return err
	}

	if eb.config.SubmitBuffer > 0 && eb.config.Engine != nil && eb.config.BeefStorage != nil {
		eb.submitCh = make(chan submitItem, eb.config.SubmitBuffer)
		workers := eb.config.SubmitWorkers
		if workers == 0 {
			workers = 1
		}
		for i := 0; i < workers; i++ {
			go eb.submitWorker(ctx)
		}
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
			member, err := parseEventMember(ev.Member)
			if err != nil {
				eb.logger.Error("failed to parse event member",
					"queue", queueKey, "member", ev.Member, "error", err)
				continue
			}
			if err := eb.config.Store.ZAdd(ctx, []byte(queueKey), store.ScoredMember{
				Member: member,
				Score:  types.HeightScore(0, 0),
			}); err != nil {
				eb.logger.Error("failed to enqueue",
					"queue", queueKey, "member", ev.Member, "error", err)
			}

			if eb.submitCh != nil {
				txid, err := extractTxid(member)
				if err == nil {
					topic := strings.TrimPrefix(queueKey, "q:")
					if _, seen := eb.submitted.LoadOrStore(*txid, struct{}{}); !seen {
						select {
						case eb.submitCh <- submitItem{txid: txid, topic: topic}:
						default:
							eb.submitted.Delete(*txid)
						}
					}
				}
			}
		}
	}
}

func (eb *EventBridge) submitWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-eb.submitCh:
			if !ok {
				return
			}
			beefBytes, err := eb.config.BeefStorage.BuildFullBeef(ctx, item.txid)
			if err != nil {
				eb.logger.Warn("direct submit: failed to build BEEF",
					"txid", item.txid.String(), "error", err)
				eb.submitted.Delete(*item.txid)
				continue
			}
			if _, err := eb.config.Engine.Submit(ctx, sdkoverlay.TaggedBEEF{
				Beef:   beefBytes,
				Topics: []string{item.topic},
			}, engine.SubmitModeHistorical, nil); err != nil {
				eb.logger.Warn("direct submit: engine.Submit failed",
					"txid", item.txid.String(), "topic", item.topic, "error", err)
			}
			eb.submitted.Delete(*item.txid)
		}
	}
}

// parseEventMember converts an event member string to binary queue member.
// Outpoint strings ("txid.vout") become 36 bytes, plain txid hex becomes 32 bytes.
func parseEventMember(member string) ([]byte, error) {
	if op, err := transaction.OutpointFromString(member); err == nil {
		return op.Bytes(), nil
	}
	txid, err := chainhash.NewHashFromHex(member)
	if err != nil {
		return nil, err
	}
	return txid[:], nil
}

// extractTxid pulls the txid from binary member bytes (first 32 bytes).
func extractTxid(member []byte) (*chainhash.Hash, error) {
	if len(member) < 32 {
		return nil, fmt.Errorf("member too short: %d", len(member))
	}
	txid := &chainhash.Hash{}
	copy(txid[:], member[:32])
	return txid, nil
}
