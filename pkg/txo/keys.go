package txo

import (
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Application-level key prefixes
const (
	PfxQueue = "q:"  // Queue keys (sorted sets used as work queues)
	PfxTopic = "tp:" // Topic namespace
	PfxEvent = "ev:" // Event namespace
)

// Bulk lookup hash keys
var (
	KeySatoshis = []byte("sats") // field: outpoint, value: satoshis (uint64 BE)
	KeySpends   = []byte("spnd") // field: outpoint, value: spend txid (32 bytes)
	KeyProgress = []byte("prog") // field: subscription/owner/peer, value: height (uint32 BE) or timestamp
)

// KeyEvent builds ZSet key for an event: ev:{event}
func KeyEvent(event string) []byte {
	return []byte(PfxEvent + event)
}

// KeyEventSpent builds ZSet key for spent event: ev:{event}:spnd
func KeyEventSpent(event string) []byte {
	return []byte(PfxEvent + event + ":spnd")
}

// Transaction log names for tracking confirmation state
const (
	PendingTxLog   = "tx:pending"   // Transactions awaiting confirmation
	ImmutableTxLog = "tx:immutable" // Confirmed transactions (100+ blocks deep)
	RollbackTxLog  = "tx:rollback"  // Rolled back transactions
)

// ImmutabilityBlocks is the number of confirmations before a tx is considered immutable
const ImmutabilityBlocks = 100

// KeyLog builds ZSet key for log entries: {logName}
// Used with OutputStore.Log() for tracking processed items
// Members are typically binary txids (32 bytes), scores are HeightScore
func KeyLog(logName string) []byte {
	return []byte(logName)
}

// KeyQueue builds queue key: q:{queueName}
// Members are binary txids (32 bytes), scores are HeightScore
func KeyQueue(queueName string) []byte {
	return []byte(PfxQueue + queueName)
}

// KeyTokenQueue builds token queue key: q:tok:{tokenId}
// Members are binary outpoints (36 bytes), scores are HeightScore
func KeyTokenQueue(tokenId string) []byte {
	return []byte(PfxQueue + "tok:" + tokenId)
}

// KeySet builds set key: {name}
func KeySet(name string) []byte {
	return []byte(name)
}

// KeyOutHash builds the hash key for an outpoint: {outpoint:36}
func KeyOutHash(op *transaction.Outpoint) []byte {
	return op.Bytes()
}

// KeyTxidPrefix builds prefix for scanning all outputs of a txid: {txid:32}
func KeyTxidPrefix(txid *chainhash.Hash) []byte {
	key := make([]byte, 32)
	copy(key, txid[:])
	return key
}
