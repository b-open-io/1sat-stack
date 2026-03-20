package pubsub

import (
	"fmt"
	"log/slog"
	"strings"
)

// CreatePubSub creates the appropriate PubSub implementation from a connection string.
// Supported formats:
//   - redis://localhost:6379 - Redis-based pub/sub
//   - channels:// - In-memory channel-based pub/sub
//   - Empty string: defaults to channels://
func CreatePubSub(connectionString string, logger *slog.Logger) (PubSub, error) {
	if connectionString == "" {
		connectionString = "channels://"
	}

	switch {
	case strings.HasPrefix(connectionString, "redis://"):
		return NewRedisPubSub(connectionString, logger)

	case strings.HasPrefix(connectionString, "channels://"):
		return NewChannelPubSub(logger), nil

	default:
		return nil, fmt.Errorf("unsupported pub/sub URL scheme: %s", connectionString)
	}
}
