package indexer

// OwnerKey returns the key for owner events
func OwnerKey(owner string) string {
	return "own:" + owner
}

// OwnerSpentKey returns the key for spent events for an owner address
func OwnerSpentKey(owner string) string {
	return "own:" + owner + ":spnd"
}

// BalanceKey returns the key for balance tracking
func BalanceKey(key string) string {
	return "bal:" + key
}

// OwnerSyncKey is the key for owner synchronization
const OwnerSyncKey = "own:sync"

// QueueKey returns the key for a queue
func QueueKey(tag string) string {
	return "que:" + tag
}

// IngestTag is the default tag for ingestion
const IngestTag = "ingest"

// IngestQueueKey is the key for the ingest queue
const IngestQueueKey = "que:ingest"

// LogKey returns the key for a log set
func LogKey(tag string) string {
	return "log:" + tag
}
