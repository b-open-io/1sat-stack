package pubsub

import (
	"context"
)

// Event represents a unified event that can come from Redis or SSE sources
type Event struct {
	Topic  string         `json:"topic"`
	Member string         `json:"member"`
	Score  float64        `json:"score"`
	Source string         `json:"source"`
	Data   map[string]any `json:"data,omitempty"`
}

// EventData represents a buffered event for reconnection
type EventData struct {
	Outpoint string  `json:"outpoint"`
	Score    float64 `json:"score"`
}

// PubSub interface for unified publishing and subscribing
type PubSub interface {
	Publish(ctx context.Context, topic string, data string, score ...float64) error
	Subscribe(ctx context.Context, topics []string) (<-chan Event, error)
	Unsubscribe(topics []string) error
	Stop() error
	Close() error
}
