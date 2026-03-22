package bsv21

import (
	"context"
	"fmt"
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
		queueDepth, err := m.redis.ZCard(ctx, jbsync.TokenQueueKey(tokenId)).Result()
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

	// Output count in topic - always calculate for reporting
	outputCount, err := m.lookup.CountOutputs(ctx, "tm_"+tokenId)
	if err != nil {
		return nil, fmt.Errorf("failed to count outputs: %w", err)
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
