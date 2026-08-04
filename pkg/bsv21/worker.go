package bsv21

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TokenWorker processes transactions for a single token
type TokenWorker struct {
	tokenId   string
	address   string
	worker    *worker.Worker
	startedAt time.Time
	cancel    context.CancelFunc
}

// WorkerStatus represents the status of a token worker for monitoring
type WorkerStatus struct {
	TokenID    string       `json:"token_id"`
	FeeAddress string       `json:"fee_address"`
	QueueDepth int64        `json:"queue_depth"`
	StartedAt  time.Time    `json:"started_at"`
	Status     *TokenStatus `json:"status,omitempty"`
}

// ListWorkers returns the status of all active token workers
func (m *TokenManager) ListWorkers(ctx context.Context) []WorkerStatus {
	var workers []WorkerStatus

	m.workers.Range(func(key, value any) bool {
		tokenId := key.(string)
		w, ok := value.(*TokenWorker)
		if !ok {
			return true
		}

		// Get queue depth
		queueKey := []byte(jbsync.TokenQueueKey(tokenId))
		queueDepth, err := m.store.ZCard(ctx, queueKey)
		if err != nil {
			m.logger.Warn("failed to get queue depth", "tokenId", tokenId, "error", err)
			queueDepth = 0
		}

		ws := WorkerStatus{
			TokenID:    tokenId,
			FeeAddress: w.address,
			QueueDepth: queueDepth,
			StartedAt:  w.startedAt,
		}

		// Include cached status if available
		if statusVal, ok := m.statuses.Load(tokenId); ok {
			ws.Status = statusVal.(*TokenStatus)
		}

		workers = append(workers, ws)
		return true
	})

	return workers
}

// outputCountKey is the config store key holding a token's persisted output count.
func outputCountKey(tokenId string) string {
	return "bsv21.outputcount:" + tokenId
}

// resolveOutputCount reports how many outputs a token has indexed, avoiding its
// topic database wherever possible. A running worker's in-memory count is
// authoritative and exact, since RecordOutput increments it per output. Failing
// that the persisted value is used. Only a token that has never been counted
// falls through to querying its database. Authoritative counts are written back
// so subsequent lifecycle passes stay off the per-topic databases entirely.
func (m *TokenManager) resolveOutputCount(ctx context.Context, tokenId string) (int64, error) {
	if v, ok := m.statuses.Load(tokenId); ok {
		count := v.(*TokenStatus).OutputCount()
		m.persistOutputCount(ctx, tokenId, count)
		return count, nil
	}

	if v, err := m.configStore.Get(ctx, outputCountKey(tokenId)); err == nil {
		if count, err := strconv.ParseInt(v, 10, 64); err == nil {
			return count, nil
		}
	}

	count, err := m.lookup.CountOutputs(ctx, "tm_"+tokenId)
	if err != nil {
		return 0, fmt.Errorf("failed to count outputs: %w", err)
	}
	m.persistOutputCount(ctx, tokenId, count)
	return count, nil
}

// persistOutputCount stores a count that came from a live worker or a real
// query. A stored value can only lag behind the truth, never exceed it, which
// makes a token look better funded than it is - corrected by the exact count
// the worker carries once it spins up.
func (m *TokenManager) persistOutputCount(ctx context.Context, tokenId string, count int64) {
	if err := m.configStore.Set(ctx, outputCountKey(tokenId), strconv.FormatInt(count, 10)); err != nil {
		m.logger.Warn("failed to persist output count", "tokenId", tokenId, "error", err)
	}
}

// GetTokenStatus returns the status for a specific token
func (m *TokenManager) GetTokenStatus(ctx context.Context, tokenId string) (*TokenStatus, error) {
	// Parse outpoint from tokenId
	outpoint, err := transaction.OutpointFromString(tokenId)
	if err != nil {
		return nil, fmt.Errorf("invalid token ID: %w", err)
	}

	// Generate fee address
	feeAddress, err := GenerateFeeAddress(outpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to generate fee address: %w", err)
	}

	// Check whitelist/blacklist status
	_, wlErr := m.configStore.Get(ctx, "bsv21.whitelist:"+tokenId)
	isWhitelisted := wlErr == nil
	_, blErr := m.configStore.Get(ctx, "bsv21.blacklist:"+tokenId)
	isBlacklisted := blErr == nil

	outputCount, err := m.resolveOutputCount(ctx, tokenId)
	if err != nil {
		return nil, err
	}

	// Whitelisted/blacklisted tokens don't need balance calculation - use 0 fee so debits = 0
	if isWhitelisted {
		return NewTokenStatus(tokenId, feeAddress, 0, outputCount, 0, true, false), nil
	}
	if isBlacklisted {
		return NewTokenStatus(tokenId, feeAddress, 0, outputCount, 0, false, true), nil
	}

	// Credits: unspent satoshis at fee address
	cfg := &txo.OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys: [][]byte{[]byte("own:" + feeAddress)},
		},
		FilterSpent: true,
	}
	credits, _, err := m.outputStore.SearchBalance(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to query balance: %w", err)
	}

	return NewTokenStatus(tokenId, feeAddress, credits, outputCount, m.feePerOutput, false, false), nil
}
