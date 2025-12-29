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

// FundingStatus represents the funding status of a token
type FundingStatus struct {
	TokenID       string `json:"token_id"`
	FeeAddress    string `json:"fee_address"`
	Credits       uint64 `json:"credits"`
	OutputCount   int64  `json:"output_count"`
	FeePerOutput  int64  `json:"fee_per_output"`
	Debits        int64  `json:"debits"`
	Balance       int64  `json:"balance"`
	IsActive      bool   `json:"is_active"`
	IsWhitelisted bool   `json:"is_whitelisted"`
	IsBlacklisted bool   `json:"is_blacklisted"`
}

// GetFundingStatus returns the funding status for a specific token
func (m *TokenManager) GetFundingStatus(ctx context.Context, tokenId string) (*FundingStatus, error) {
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
	isWhitelisted, _ := m.store.SIsMember(ctx, KeyWhitelist, []byte(tokenId))

	// If whitelisted or blacklisted, we don't need balance info
	if isWhitelisted {
		return &FundingStatus{
			TokenID:       tokenId,
			FeeAddress:    feeAddress,
			IsActive:      true,
			IsWhitelisted: true,
		}, nil
	}
	isBlacklisted, _ := m.store.SIsMember(ctx, KeyBlacklist, []byte(tokenId))
	if isBlacklisted {
		return &FundingStatus{
			TokenID:       tokenId,
			FeeAddress:    feeAddress,
			IsActive:      false,
			IsBlacklisted: true,
		}, nil
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

	// Debits: output count * fee per output
	topicKey := []byte("z:tp:tm_" + tokenId)
	outputCount, err := m.store.ZCard(ctx, topicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to count outputs: %w", err)
	}

	debits := outputCount * m.feePerOutput
	balance := int64(credits) - debits

	return &FundingStatus{
		TokenID:      tokenId,
		FeeAddress:   feeAddress,
		Credits:      credits,
		OutputCount:  outputCount,
		FeePerOutput: m.feePerOutput,
		Debits:       debits,
		Balance:      balance,
		IsActive:     balance > 0,
	}, nil
}
