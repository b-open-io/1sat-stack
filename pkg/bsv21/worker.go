package bsv21

import (
	"context"
	"fmt"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/worker"
)

// TokenWorker processes transactions for a single token
type TokenWorker struct {
	tokenId   string
	address   string
	worker    *worker.Worker
	startedAt time.Time
}

// WorkerStatus represents the status of a token worker for monitoring
type WorkerStatus struct {
	TokenID    string    `json:"token_id"`
	FeeAddress string    `json:"fee_address"`
	QueueDepth int64     `json:"queue_depth"`
	StartedAt  time.Time `json:"started_at"`
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

		workers = append(workers, WorkerStatus{
			TokenID:    tokenId,
			FeeAddress: w.address,
			QueueDepth: queueDepth,
			StartedAt:  w.startedAt,
		})
		return true
	})

	return workers
}

// calculateBalance calculates credits - debits for a token
func (m *TokenManager) calculateBalance(ctx context.Context, tokenId, feeAddress string) (int64, error) {
	// Credits: unspent satoshis at fee address
	cfg := &txo.OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys: [][]byte{[]byte("own:" + feeAddress)},
		},
		FilterSpent: true,
	}
	credits, _, err := m.outputStore.SearchBalance(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("failed to query balance: %w", err)
	}

	// Debits: output count * fee per output
	topicKey := []byte("z:tp:tm_" + tokenId)
	outputCount, err := m.store.ZCard(ctx, topicKey)
	if err != nil {
		return 0, fmt.Errorf("failed to count outputs: %w", err)
	}

	debits := outputCount * m.feePerOutput
	balance := int64(credits) - debits

	m.logger.Debug("balance calculated",
		"tokenId", tokenId,
		"feeAddress", feeAddress,
		"credits", credits,
		"outputCount", outputCount,
		"debits", debits,
		"balance", balance)

	return balance, nil
}
