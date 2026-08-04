package bsv21

import (
	"encoding/json"
	"sync/atomic"
)

// TokenStatus holds all token state - identity, list status, funding metrics, and live balance tracking.
// Used both for internal tracking and API responses.
type TokenStatus struct {
	// Identity
	TokenID    string `json:"token_id"`
	FeeAddress string `json:"fee_address"`

	// Token metadata
	Symbol   *string `json:"symbol,omitempty"`
	Decimals *uint8  `json:"decimals,omitempty"`
	Icon     *string `json:"icon,omitempty"`

	// List status
	IsWhitelisted bool `json:"is_whitelisted"`
	IsBlacklisted bool `json:"is_blacklisted"`

	// Funding metrics (from DB on creation/recalc)
	Credits      uint64 `json:"credits"`
	FeePerOutput int64  `json:"fee_per_output"`

	// Live tracking (not serialized directly). outputCount is atomic because
	// workers record indexed outputs concurrently.
	outputCount atomic.Int64
	balance     atomic.Int64
	syncing     atomic.Bool
}

// NewTokenStatus creates a TokenStatus and initializes the atomic balance
func NewTokenStatus(tokenId, feeAddress string, credits uint64, outputCount, feePerOutput int64, isWhitelisted, isBlacklisted bool) *TokenStatus {
	ts := &TokenStatus{
		TokenID:       tokenId,
		FeeAddress:    feeAddress,
		IsWhitelisted: isWhitelisted,
		IsBlacklisted: isBlacklisted,
		Credits:       credits,
		FeePerOutput:  feePerOutput,
	}
	ts.outputCount.Store(outputCount)
	ts.balance.Store(int64(credits) - ts.Debits())
	return ts
}

// Balance returns the current live balance
func (ts *TokenStatus) Balance() int64 {
	return ts.balance.Load()
}

// OutputCount returns the number of outputs indexed for this token.
func (ts *TokenStatus) OutputCount() int64 {
	return ts.outputCount.Load()
}

// SetOutputCount replaces the count after a recalculation from the database.
func (ts *TokenStatus) SetOutputCount(n int64) {
	ts.outputCount.Store(n)
}

// Debits returns the total fees charged for the outputs indexed so far.
func (ts *TokenStatus) Debits() int64 {
	return ts.outputCount.Load() * ts.FeePerOutput
}

// IsActive returns whether the token should be processing
func (ts *TokenStatus) IsActive() bool {
	if ts.IsWhitelisted {
		return true
	}
	if ts.IsBlacklisted {
		return false
	}
	return ts.balance.Load() > 0
}

// RecordOutput accounts for a single indexed output: it increments the output
// count and charges feePerOutput against the balance, returning the new balance.
// One queue member is one outpoint, so this is called once per output.
func (ts *TokenStatus) RecordOutput() int64 {
	ts.outputCount.Add(1)
	return ts.balance.Add(-ts.FeePerOutput)
}

// UpdateBalance sets a new balance (after recalculation from DB)
func (ts *TokenStatus) UpdateBalance(newBalance int64) {
	ts.balance.Store(newBalance)
}

// TryStartSync attempts to acquire sync lock. Returns true if acquired.
func (ts *TokenStatus) TryStartSync() bool {
	return ts.syncing.CompareAndSwap(false, true)
}

// EndSync releases the sync lock
func (ts *TokenStatus) EndSync() {
	ts.syncing.Store(false)
}

// MarshalJSON includes the fields computed from atomics rather than stored
func (ts *TokenStatus) MarshalJSON() ([]byte, error) {
	type Alias TokenStatus
	return json.Marshal(&struct {
		OutputCount int64 `json:"output_count"`
		Debits      int64 `json:"debits"`
		Balance     int64 `json:"balance"`
		IsActive    bool  `json:"is_active"`
		*Alias
	}{
		OutputCount: ts.OutputCount(),
		Debits:      ts.Debits(),
		Balance:     ts.balance.Load(),
		IsActive:    ts.IsActive(),
		Alias:       (*Alias)(ts),
	})
}
