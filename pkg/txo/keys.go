package txo

import (
	"fmt"

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

// KeyTopicOutputs builds ZSet key for topic outputs: tp:{topic}
// Members are binary outpoints (36 bytes), scores are HeightScore
func KeyTopicOutputs(topic string) []byte {
	return []byte(PfxTopic + topic)
}

// KeyTopicTxs builds ZSet key for topic applied txids: tp:{topic}:tx
// Members are binary txids (32 bytes), scores are HeightScore
func KeyTopicTxs(topic string) []byte {
	return []byte(PfxTopic + topic + ":tx")
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

// KeyMerkleState builds ZSet key for merkle state index: merkle:{topic}:{state}
// Members are binary outpoints (36 bytes), scores are HeightScore
func KeyMerkleState(topic string, state uint32) []byte {
	return []byte(fmt.Sprintf("merkle:%s:%d", topic, state))
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

// PeerInteractionField builds the field name for peer interaction progress: peer:{topic}:{host}
// Used with KeyProgress hash
func PeerInteractionField(topic, host string) []byte {
	return []byte("peer:" + topic + ":" + host)
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
