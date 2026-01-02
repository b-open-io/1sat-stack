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

	// List status
	IsWhitelisted bool `json:"is_whitelisted"`
	IsBlacklisted bool `json:"is_blacklisted"`

	// Funding metrics (from DB on creation/recalc)
	Credits      uint64 `json:"credits"`
	OutputCount  int64  `json:"output_count"`
	FeePerOutput int64  `json:"fee_per_output"`
	Debits       int64  `json:"debits"`

	// Live tracking (not serialized directly)
	balance atomic.Int64
	syncing atomic.Bool
}

// NewTokenStatus creates a TokenStatus and initializes the atomic balance
func NewTokenStatus(tokenId, feeAddress string, credits uint64, outputCount, feePerOutput int64, isWhitelisted, isBlacklisted bool) *TokenStatus {
	ts := &TokenStatus{
		TokenID:       tokenId,
		FeeAddress:    feeAddress,
		IsWhitelisted: isWhitelisted,
		IsBlacklisted: isBlacklisted,
		Credits:       credits,
		OutputCount:   outputCount,
		FeePerOutput:  feePerOutput,
		Debits:        outputCount * feePerOutput,
	}
	ts.balance.Store(int64(credits) - ts.Debits)
	return ts
}

// Balance returns the current live balance
func (ts *TokenStatus) Balance() int64 {
	return ts.balance.Load()
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

// Deduct atomically decrements balance by feePerOutput, returns new balance
func (ts *TokenStatus) Deduct() int64 {
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

// MarshalJSON includes computed Balance and IsActive fields
func (ts *TokenStatus) MarshalJSON() ([]byte, error) {
	type Alias TokenStatus
	return json.Marshal(&struct {
		Balance  int64 `json:"balance"`
		IsActive bool  `json:"is_active"`
		*Alias
	}{
		Balance:  ts.balance.Load(),
		IsActive: ts.IsActive(),
		Alias:    (*Alias)(ts),
	})
}
