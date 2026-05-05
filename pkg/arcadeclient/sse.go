package arcadeclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Subscribe opens a long-lived SSE connection to arcade /events using the client's callback token.
//
// Events arrive on the returned channel until ctx is cancelled. The consumer reconnects
// automatically on transient errors (with exponential backoff up to 30s), passing the
// last-seen event ID via Last-Event-ID so missed events can replay.
//
// Pass an initial lastEventID to resume from a checkpoint; pass "" to start from now.
// The channel is closed when ctx is cancelled or the connection cannot be re-established.
func (c *Client) Subscribe(ctx context.Context, lastEventID string) <-chan *SSEEvent {
	ch := make(chan *SSEEvent, 32)
	go c.runSubscription(ctx, lastEventID, ch)
	return ch
}

func (c *Client) runSubscription(ctx context.Context, initialLastID string, ch chan<- *SSEEvent) {
	defer close(ch)

	const (
		initialBackoff = time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff
	lastID := initialLastID

	for {
		if ctx.Err() != nil {
			return
		}

		nextID, err := c.consumeStream(ctx, lastID, ch)
		if nextID != "" {
			lastID = nextID
			backoff = initialBackoff // reset backoff once we successfully read at least one event
		}

		if ctx.Err() != nil {
			return
		}

		c.logger.Warn("arcade SSE disconnected, will reconnect",
			"err", err, "last_event_id", lastID, "retry_in", backoff.String())

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// consumeStream reads from arcade's /events SSE endpoint until disconnect or ctx cancellation.
// Returns the last event ID emitted on the channel and any error encountered while reading.
func (c *Client) consumeStream(ctx context.Context, lastID string, ch chan<- *SSEEvent) (string, error) {
	endpoint := c.baseURL + "/events"
	if c.callbackToken != "" {
		endpoint += "?callbackToken=" + url.QueryEscape(c.callbackToken)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return lastID, fmt.Errorf("build SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return lastID, fmt.Errorf("open SSE: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return lastID, fmt.Errorf("arcade SSE returned %d", resp.StatusCode)
	}

	c.logger.Info("arcade SSE connected", "last_event_id", lastID)

	reader := bufio.NewReader(resp.Body)
	var (
		currentID    string
		currentEvent string
		currentData  strings.Builder
		emittedID    = lastID
	)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return emittedID, fmt.Errorf("read SSE: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")

		// Blank line terminates an event frame.
		if line == "" {
			if currentData.Len() > 0 && (currentEvent == "" || currentEvent == "status") {
				evt := &SSEEvent{ID: currentID}
				if err := json.Unmarshal([]byte(currentData.String()), evt); err != nil {
					c.logger.Warn("arcade SSE: failed to parse event data",
						"data", currentData.String(), "err", err)
				} else {
					select {
					case ch <- evt:
						if currentID != "" {
							emittedID = currentID
						}
					case <-ctx.Done():
						return emittedID, ctx.Err()
					}
				}
			}
			currentID = ""
			currentEvent = ""
			currentData.Reset()
			continue
		}

		// Comment frame (e.g. ": keepalive")
		if strings.HasPrefix(line, ":") {
			continue
		}

		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		field := line[:idx]
		value := strings.TrimPrefix(line[idx+1:], " ")
		switch field {
		case "id":
			currentID = value
		case "event":
			currentEvent = value
		case "data":
			if currentData.Len() > 0 {
				currentData.WriteByte('\n')
			}
			currentData.WriteString(value)
		}
	}
}
