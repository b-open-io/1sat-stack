package overlay

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/types"
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
			eb.logger.Info("bridging event", "topic", ev.Topic, "member", ev.Member, "queue", queueKey)
			if err := eb.config.Store.ZAdd(ctx, []byte(queueKey), store.ScoredMember{
				Member: []byte(ev.Member),
				Score:  types.HeightScore(0, 0),
			}); err != nil {
				eb.logger.Error("failed to enqueue txid",
					"queue", queueKey, "txid", ev.Member, "error", err)
			}
		}
	}
}
