package collection

import (
	"encoding/json"
	"sync/atomic"
)

// CollectionStatus is the funding state of one collection.
// It is the collection equivalent of bsv21.TokenStatus: identity, list
// membership, credits at the fee address, and the live balance.
type CollectionStatus struct {
	CollectionID string `json:"collection_id"`
	FeeAddress   string `json:"fee_address"`
	Name         string `json:"name,omitempty"`

	IsWhitelisted bool `json:"is_whitelisted"`
	IsBlacklisted bool `json:"is_blacklisted"`

	Credits      uint64 `json:"credits"`
	FeePerOutput int64  `json:"fee_per_output"`

	outputCount atomic.Int64
	balance     atomic.Int64
	syncing     atomic.Bool
	// forced means a test or operator switch is indexing this collection
	// regardless of funding. Blacklist still wins.
	forced bool
}

// Balance returns the current live balance.
func (s *CollectionStatus) Balance() int64 {
	return s.balance.Load()
}

// OutputCount returns the number of item outputs indexed for this collection.
func (s *CollectionStatus) OutputCount() int64 {
	return s.outputCount.Load()
}

// SetOutputCount replaces the count after a recalculation.
func (s *CollectionStatus) SetOutputCount(n int64) {
	s.outputCount.Store(n)
}

// Debits returns the fees charged for the outputs indexed so far.
func (s *CollectionStatus) Debits() int64 {
	return s.outputCount.Load() * s.FeePerOutput
}

// IsActive reports whether an item worker should be running.
func (s *CollectionStatus) IsActive() bool {
	if s.IsBlacklisted {
		return false
	}
	if s.IsWhitelisted || s.forced {
		return true
	}
	return s.balance.Load() > 0
}

// RecordOutput accounts for one indexed item and returns the new balance.
func (s *CollectionStatus) RecordOutput() int64 {
	s.outputCount.Add(1)
	return s.balance.Add(-s.FeePerOutput)
}

// UpdateBalance sets the balance after a recalculation from the database.
func (s *CollectionStatus) UpdateBalance(newBalance int64) {
	s.balance.Store(newBalance)
}

// TryStartSync acquires the fee-address sync lock.
func (s *CollectionStatus) TryStartSync() bool {
	return s.syncing.CompareAndSwap(false, true)
}

// EndSync releases the fee-address sync lock.
func (s *CollectionStatus) EndSync() {
	s.syncing.Store(false)
}

// MarshalJSON includes the fields computed from atomics.
func (s *CollectionStatus) MarshalJSON() ([]byte, error) {
	type Alias CollectionStatus
	return json.Marshal(&struct {
		OutputCount int64 `json:"output_count"`
		Debits      int64 `json:"debits"`
		Balance     int64 `json:"balance"`
		IsActive    bool  `json:"is_active"`
		*Alias
	}{
		OutputCount: s.OutputCount(),
		Debits:      s.Debits(),
		Balance:     s.balance.Load(),
		IsActive:    s.IsActive(),
		Alias:       (*Alias)(s),
	})
}
