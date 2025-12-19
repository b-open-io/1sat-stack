package jbsync

const (
	// PfxQueue is the prefix for queue sorted sets
	PfxQueue = "q:"

	// KeyProgress is the hash key for sync progress tracking
	// Fields are subscription IDs or addresses, values are last synced block heights
	KeyProgress = "sync:progress"
)

// QueueKey returns the queue key for a subscription or queue name
func QueueKey(queueName string) string {
	return PfxQueue + queueName
}

// TokenQueueKey returns the queue key for a token-specific queue
func TokenQueueKey(tokenId string) string {
	return PfxQueue + "tok:" + tokenId
}
