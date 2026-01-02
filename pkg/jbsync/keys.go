package jbsync

import "github.com/b-open-io/1sat-stack/pkg/txo"

// Re-export key builders from txo for convenience
var (
	KeyProgress = txo.KeyProgress
	KeyQueue    = txo.KeyQueue
)

// QueueKey returns the queue key for a subscription or queue name (string version)
func QueueKey(queueName string) string {
	return string(txo.KeyQueue(queueName))
}

// TokenQueueKey returns the queue key for a token topic queue (string version)
// Format: q:tm_{tokenId} (topic queue format)
func TokenQueueKey(tokenId string) string {
	return "q:tm_" + tokenId
}
