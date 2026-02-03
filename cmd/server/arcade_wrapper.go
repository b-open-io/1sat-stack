package main

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bsv-blockchain/arcade/models"
	"github.com/bsv-blockchain/arcade/service"
)

// BeefCapturingArcadeService wraps an ArcadeService to capture BEEF data before submission.
// This decorator pattern eliminates the need for submission events by saving the raw
// transaction data at the point of submission.
type BeefCapturingArcadeService struct {
	inner       service.ArcadeService
	beefStorage *beef.Storage
	logger      *slog.Logger
}

// NewBeefCapturingArcadeService creates a new wrapper that captures BEEF before delegating to inner.
func NewBeefCapturingArcadeService(
	inner service.ArcadeService,
	beefStorage *beef.Storage,
	logger *slog.Logger,
) *BeefCapturingArcadeService {
	if logger == nil {
		logger = slog.Default()
	}
	return &BeefCapturingArcadeService{
		inner:       inner,
		beefStorage: beefStorage,
		logger:      logger,
	}
}

// SubmitTransaction saves BEEF before delegating to the inner service.
func (s *BeefCapturingArcadeService) SubmitTransaction(ctx context.Context, rawTx []byte, opts *models.SubmitOptions) (*models.TransactionStatus, error) {
	// Save BEEF before submission
	if s.beefStorage != nil {
		if err := s.beefStorage.SaveRaw(ctx, rawTx); err != nil {
			s.logger.Warn("failed to save BEEF", "error", err)
		}
	}
	return s.inner.SubmitTransaction(ctx, rawTx, opts)
}

// SubmitTransactions saves BEEF for each transaction before delegating to the inner service.
func (s *BeefCapturingArcadeService) SubmitTransactions(ctx context.Context, rawTxs [][]byte, opts *models.SubmitOptions) ([]*models.TransactionStatus, error) {
	// Save BEEF for each transaction before submission
	if s.beefStorage != nil {
		for _, rawTx := range rawTxs {
			if err := s.beefStorage.SaveRaw(ctx, rawTx); err != nil {
				s.logger.Warn("failed to save BEEF", "error", err)
			}
		}
	}
	return s.inner.SubmitTransactions(ctx, rawTxs, opts)
}

// GetStatus delegates to the inner service.
func (s *BeefCapturingArcadeService) GetStatus(ctx context.Context, txid string) (*models.TransactionStatus, error) {
	return s.inner.GetStatus(ctx, txid)
}

// Subscribe delegates to the inner service.
func (s *BeefCapturingArcadeService) Subscribe(ctx context.Context, callbackToken string) (<-chan *models.TransactionStatus, error) {
	return s.inner.Subscribe(ctx, callbackToken)
}

// Unsubscribe delegates to the inner service.
func (s *BeefCapturingArcadeService) Unsubscribe(ch <-chan *models.TransactionStatus) {
	s.inner.Unsubscribe(ch)
}

// GetPolicy delegates to the inner service.
func (s *BeefCapturingArcadeService) GetPolicy(ctx context.Context) (*models.Policy, error) {
	return s.inner.GetPolicy(ctx)
}
