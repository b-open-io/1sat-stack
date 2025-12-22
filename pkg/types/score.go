package types

import (
	"time"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// HeightScore calculates a score for ordering transactions.
// This provides a unified scoring convention used across all sorted sets.
//
// Format: integer_part.fractional_precision
//   - Confirmed tx: block height + tx index as decimal (e.g., 800000.000001234)
//   - Mempool tx: unix timestamp + nanoseconds as decimal (e.g., 1734567890.123456789)
//
// Why this works:
//   - Current unix timestamps (~1.7e9) >> block heights (~800k), so mempool always sorts after confirmed
//   - float64 has ~15-16 significant digits, plenty of precision
//   - Precision loss only affects the fractional part (tx index or nanoseconds) - acceptable
//   - Human-readable when debugging
func HeightScore(height uint32, idx uint64) float64 {
	if height == 0 {
		// Mempool/unconfirmed: unix timestamp with nanosecond precision
		return float64(time.Now().UnixNano()) / 1e9
	}
	// Confirmed: block height + tx index as decimal
	return float64(height) + float64(idx)/1e9
}

// ScoreFromTx extracts the score from a transaction's MerklePath.
// If the transaction is unconfirmed (no MerklePath), returns a timestamp-based score.
func ScoreFromTx(tx *transaction.Transaction, txid *chainhash.Hash) float64 {
	if tx == nil || tx.MerklePath == nil || len(tx.MerklePath.Path) == 0 {
		return HeightScore(0, 0)
	}

	// Find the tx's position (offset) in the block from the merkle path leaf
	var blockIdx uint64
	for _, leaf := range tx.MerklePath.Path[0] {
		if leaf.Hash != nil && txid.IsEqual(leaf.Hash) {
			blockIdx = leaf.Offset
			break
		}
	}

	return HeightScore(tx.MerklePath.BlockHeight, blockIdx)
}

// ScoreFromBeef parses BEEF data and extracts the score.
// If parsing fails or tx is unconfirmed, returns a timestamp-based score.
func ScoreFromBeef(beefData []byte) float64 {
	if len(beefData) == 0 {
		return HeightScore(0, 0)
	}

	_, tx, txid, err := transaction.ParseBeef(beefData)
	if err != nil {
		return HeightScore(0, 0)
	}

	return ScoreFromTx(tx, txid)
}
