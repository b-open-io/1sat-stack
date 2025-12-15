package pubsub

import (
	"fmt"
	"strings"
)

// CreatePubSub creates the appropriate PubSub implementation from a connection string.
// Supported formats:
//   - redis://localhost:6379 - Redis-based pub/sub
//   - channels:// - In-memory channel-based pub/sub
//   - Empty string: defaults to channels://
func CreatePubSub(connectionString string) (PubSub, error) {
	if connectionString == "" {
		connectionString = "channels://"
	}

	switch {
	case strings.HasPrefix(connectionString, "redis://"):
		// TODO: Implement Redis pub/sub
		return nil, fmt.Errorf("redis pub/sub not yet implemented")

	case strings.HasPrefix(connectionString, "channels://"):
		return NewChannelPubSub(), nil

	default:
		return nil, fmt.Errorf("unsupported pub/sub URL scheme: %s", connectionString)
	}
}
