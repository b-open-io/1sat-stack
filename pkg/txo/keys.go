package txo

import (
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Key prefixes - exported for cross-package use
const (
	PfxHash  = "h:"  // Hash keys
	PfxZSet  = "z:"  // Sorted set keys
	PfxSet   = "s:"  // Set keys
	PfxQueue = "q:"  // Queue keys (sorted sets used as work queues)
	PfxTopic = "tp:" // Topic prefix within ZSet
)

// Bulk lookup hash keys
// These are single hash keys where fields are outpoints (36 bytes)
var (
	KeySatoshis = []byte(PfxHash + "sats") // h:sats - field: outpoint, value: satoshis (uint64 BE)
	KeySpends   = []byte(PfxHash + "spnd") // h:spnd - field: outpoint, value: spend txid (32 bytes)
	KeyProgress = []byte(PfxHash + "prog") // h:prog - field: subscription/owner/peer, value: height (uint32 BE) or timestamp
)

// KeyEvent builds ZSet key for an event: z:{event}
// Use this for event-based lookups (owner addresses, token IDs, etc.)
func KeyEvent(event string) []byte {
	return []byte(PfxZSet + event)
}

// KeyEventSpent builds ZSet key for spent event: z:{event}:spnd
func KeyEventSpent(event string) []byte {
	return []byte(PfxZSet + event + ":spnd")
}

// KeyTopicOutputs builds ZSet key for topic outputs: z:tp:{topic}
// Members are binary outpoints (36 bytes), scores are HeightScore
func KeyTopicOutputs(topic string) []byte {
	return []byte(PfxZSet + PfxTopic + topic)
}

// KeyTopicTxs builds ZSet key for topic applied txids: z:tp:{topic}:tx
// Members are binary txids (32 bytes), scores are HeightScore
func KeyTopicTxs(topic string) []byte {
	return []byte(PfxZSet + PfxTopic + topic + ":tx")
}

// KeyLog builds ZSet key for log entries: z:{logName}
// Used with OutputStore.Log() for tracking processed items
// Members are typically binary txids (32 bytes), scores are HeightScore
func KeyLog(logName string) []byte {
	return []byte(PfxZSet + logName)
}

// KeyMerkleState builds ZSet key for merkle state index: z:merkle:{topic}:{state}
// Members are binary outpoints (36 bytes), scores are HeightScore
func KeyMerkleState(topic string, state uint32) []byte {
	return []byte(fmt.Sprintf("%smerkle:%s:%d", PfxZSet, topic, state))
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

// KeySet builds set key: s:{name}
func KeySet(name string) []byte {
	return []byte(PfxSet + name)
}

// PeerInteractionField builds the field name for peer interaction progress: peer:{topic}:{host}
// Used with KeyProgress hash
func PeerInteractionField(topic, host string) []byte {
	return []byte("peer:" + topic + ":" + host)
}

// KeyOutHash builds the hash key for an outpoint: h:{outpoint:36}
func KeyOutHash(op *transaction.Outpoint) []byte {
	key := make([]byte, 2+36) // "h:" + 36 byte outpoint
	copy(key, PfxHash)
	copy(key[2:], op.Bytes())
	return key
}

// KeyTxidPrefix builds prefix for scanning all outputs of a txid: h:{txid:32}
func KeyTxidPrefix(txid *chainhash.Hash) []byte {
	key := make([]byte, 2+32) // "h:" + 32 byte txid
	copy(key, PfxHash)
	copy(key[2:], txid[:])
	return key
}
