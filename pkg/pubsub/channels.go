package pubsub

import (
	"context"
	"log/slog"
	"sync"
)

const eventChannelBuffer = 100

type channelSubscription struct {
	ctx     context.Context
	channel chan Event
	topics  []string
}

// ChannelPubSub implements the PubSub interface using Go channels
type ChannelPubSub struct {
	subscribers sync.Map // topic -> []*channelSubscription
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *slog.Logger
}

func NewChannelPubSub(logger *slog.Logger) *ChannelPubSub {
	ctx, cancel := context.WithCancel(context.Background())

	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "pubsub")

	return &ChannelPubSub{
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}
}

func (cp *ChannelPubSub) Publish(ctx context.Context, topic string, data string, score ...float64) error {
	var eventScore float64 = 0
	if len(score) > 0 {
		eventScore = score[0]
	}

	event := Event{
		Topic:  topic,
		Member: data,
		Score:  eventScore,
		Source: "channels",
	}

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

	return nil
}

func (cp *ChannelPubSub) Subscribe(ctx context.Context, topics []string) (<-chan Event, error) {
	eventChan := make(chan Event, eventChannelBuffer)

	sub := &channelSubscription{
		ctx:     ctx,
		channel: eventChan,
		topics:  topics,
	}

	for _, topic := range topics {
		var subs []*channelSubscription
		if existing, ok := cp.subscribers.Load(topic); ok {
			subs = existing.([]*channelSubscription)
		}
		subs = append(subs, sub)
		cp.subscribers.Store(topic, subs)
	}

	go func() {
		<-ctx.Done()
		cp.unsubscribeSubscription(sub)
		close(eventChan)
	}()

	return eventChan, nil
}

func (cp *ChannelPubSub) Unsubscribe(topics []string) error {
	return nil
}

func (cp *ChannelPubSub) unsubscribeSubscription(targetSub *channelSubscription) {
	for _, topic := range targetSub.topics {
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

func (cp *ChannelPubSub) Stop() error {
	cp.cancel()
	return nil
}

func (cp *ChannelPubSub) Close() error {
	cp.cancel()
	return nil
}
