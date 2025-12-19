package types

import "time"

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
