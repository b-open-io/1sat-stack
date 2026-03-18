package overlay

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type EventBridgeConfig struct {
	PubSub    pubsub.PubSub
	Store     store.Store
	Patterns  []string
	QueueFunc func(pubsub.Event) string
	Logger    *slog.Logger
}

type EventBridge struct {
	config *EventBridgeConfig
	logger *slog.Logger
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
