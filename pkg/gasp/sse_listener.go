// Package gasp provides GASP (Graph Aware Sync Protocol) implementations for topic-based sync.
package gasp

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// SSEListenerConfig configures an SSE listener for a topic.
type SSEListenerConfig struct {
	// PeerURL is the peer's stack API base (e.g. "https://api.1sat.app/1sat").
	// The listener requests GET {PeerURL}/sse/{TopicName}.
	PeerURL string
	// TopicName is the topic to subscribe to
	TopicName string
	// QueueKey is the key for the local queue to write incoming items
	QueueKey []byte
	// Store is the store for queue operations
	Store store.Store
	// HTTPClient is the HTTP client to use (optional, defaults to http.DefaultClient)
	HTTPClient *http.Client
	// Logger is the logger to use
	Logger *slog.Logger
	// ReconnectDelay is the delay between reconnection attempts (default: 5s)
	ReconnectDelay time.Duration
}

// SSEListener listens to a remote peer's SSE stream and queues incoming events.
type SSEListener struct {
	config *SSEListenerConfig
	logger *slog.Logger
	cancel context.CancelFunc
}

// NewSSEListener creates a new SSE listener.
func NewSSEListener(cfg *SSEListenerConfig) *SSEListener {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 5 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SSEListener{
		config: cfg,
		logger: logger.With("component", "sse-listener", "peer", cfg.PeerURL, "topic", cfg.TopicName),
	}
}

// Start begins listening to the SSE stream. Blocks until context is cancelled.
func (l *SSEListener) Start(ctx context.Context) error {
	ctx, l.cancel = context.WithCancel(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := l.connect(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				l.logger.Warn("SSE connection failed, reconnecting", "error", err, "delay", l.config.ReconnectDelay)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(l.config.ReconnectDelay):
					continue
				}
			}
		}
	}
}

// Stop stops the SSE listener.
func (l *SSEListener) Stop() {
	if l.cancel != nil {
		l.cancel()
	}
}

// connect establishes an SSE connection and processes events.
func (l *SSEListener) connect(ctx context.Context) error {
	// Match pkg/pubsub routes: GET {base}/sse/{topics} (comma-separated topics).
	url := fmt.Sprintf("%s/sse/%s", strings.TrimRight(l.config.PeerURL, "/"), l.config.TopicName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	resp, err := l.config.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	l.logger.Info("SSE connection established")

	scanner := bufio.NewScanner(resp.Body)
	var eventType, eventData string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event
			if eventData != "" {
				if err := l.handleEvent(ctx, eventType, eventData); err != nil {
					l.logger.Error("failed to handle event", "error", err, "type", eventType)
				}
			}
			eventType = ""
			eventData = ""
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			eventData = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		// Ignore id: and retry: fields for now
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	return nil
}

// handleEvent processes an incoming SSE event.
func (l *SSEListener) handleEvent(ctx context.Context, eventType, data string) error {
	// Event type should match topic name, data should be outpoint in ordinal format (txid_vout)
	if eventType != l.config.TopicName && eventType != "" {
		// Ignore events for other topics
		return nil
	}

	// Parse outpoint from data (expected format: "txid_vout" in hex)
	outpoint, err := parseOutpoint(data)
	if err != nil {
		l.logger.Debug("failed to parse outpoint", "data", data, "error", err)
		return nil // Don't fail on parse errors, just skip
	}

	// Queue the outpoint for processing
	// Use current time as score (will be processed in order received)
	if err := l.config.Store.ZAdd(ctx, l.config.QueueKey, store.ScoredMember{
		Member: outpoint.Bytes(),
		Score:  float64(time.Now().Unix()),
	}); err != nil {
		return fmt.Errorf("failed to queue outpoint: %w", err)
	}

	l.logger.Debug("queued outpoint from SSE", "outpoint", outpoint.String())
	return nil
}

// parseOutpoint parses an outpoint from ordinal string format "txid_vout".
func parseOutpoint(s string) (*transaction.Outpoint, error) {
	return transaction.OutpointFromString(s)
}
