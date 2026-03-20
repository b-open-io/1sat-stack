package pubsub

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

// RedisPubSub implements the PubSub interface using Redis pub/sub.
type RedisPubSub struct {
	client *redis.Client
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
	mu     sync.Mutex
	subs   []*redis.PubSub
}

// RedisConfig holds Redis pub/sub configuration.
type RedisConfig struct {
	URL string `mapstructure:"url"`
}

func NewRedisPubSub(url string, logger *slog.Logger) (*RedisPubSub, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithCancel(context.Background())

	if err := client.Ping(ctx).Err(); err != nil {
		cancel()
		client.Close()
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "pubsub-redis")

	return &RedisPubSub{
		client: client,
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}, nil
}

func (rp *RedisPubSub) Publish(ctx context.Context, topic string, data string, score ...float64) error {
	event := Event{
		Topic:  topic,
		Member: data,
		Source: "redis",
	}
	if len(score) > 0 {
		event.Score = score[0]
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return rp.client.Publish(ctx, topic, payload).Err()
}

func (rp *RedisPubSub) Subscribe(ctx context.Context, topics []string) (<-chan Event, error) {
	eventChan := make(chan Event, eventChannelBuffer)

	var exactTopics []string
	var patterns []string
	for _, t := range topics {
		if strings.HasSuffix(t, "*") {
			patterns = append(patterns, t)
		} else {
			exactTopics = append(exactTopics, t)
		}
	}

	var redisSubs []*redis.PubSub
	deliver := func(msg *redis.Message) {
		var event Event
		if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
			event = Event{
				Topic:  msg.Channel,
				Member: msg.Payload,
				Source: "redis",
			}
		}
		select {
		case eventChan <- event:
		case <-ctx.Done():
		default:
			rp.logger.Warn("skipping full channel", "topic", msg.Channel)
		}
	}

	if len(exactTopics) > 0 {
		sub := rp.client.Subscribe(rp.ctx, exactTopics...)
		redisSubs = append(redisSubs, sub)
		go func() {
			ch := sub.Channel()
			for msg := range ch {
				deliver(msg)
			}
		}()
	}

	if len(patterns) > 0 {
		sub := rp.client.PSubscribe(rp.ctx, patterns...)
		redisSubs = append(redisSubs, sub)
		go func() {
			ch := sub.Channel()
			for msg := range ch {
				deliver(msg)
			}
		}()
	}

	rp.mu.Lock()
	rp.subs = append(rp.subs, redisSubs...)
	rp.mu.Unlock()

	go func() {
		<-ctx.Done()
		for _, sub := range redisSubs {
			sub.Close()
		}
		close(eventChan)
	}()

	return eventChan, nil
}

func (rp *RedisPubSub) Unsubscribe(topics []string) error {
	return nil
}

func (rp *RedisPubSub) Stop() error {
	rp.cancel()
	return nil
}

func (rp *RedisPubSub) Close() error {
	rp.cancel()
	rp.mu.Lock()
	for _, sub := range rp.subs {
		sub.Close()
	}
	rp.subs = nil
	rp.mu.Unlock()
	return rp.client.Close()
}
