package main

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bsv-blockchain/arcade/models"
	"github.com/bsv-blockchain/arcade/service"
	sdkTx "github.com/bsv-blockchain/go-sdk/transaction"
)

// BeefCapturingArcadeService wraps an ArcadeService to capture BEEF data before submission.
// When a raw transaction (not BEEF) is submitted, it automatically builds a BEEF envelope
// with ancestor transactions from storage, enabling arcade to submit ancestors to teranode.
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

// enrichWithBeef takes raw transaction bytes and attempts to build a BEEF envelope.
// If the input is already BEEF, it saves and returns it as-is.
// If it's a raw tx, it saves it, populates ancestors from storage, and returns BEEF bytes.
// On any failure, it returns the original raw bytes.
func (s *BeefCapturingArcadeService) enrichWithBeef(ctx context.Context, rawTx []byte) []byte {
	if s.beefStorage == nil {
		return rawTx
	}

	// Check if already BEEF
	_, tx, _, err := sdkTx.ParseBeef(rawTx)
	if err == nil && tx != nil {
		// Already BEEF — save and pass through
		if err := s.beefStorage.SaveRaw(ctx, rawTx); err != nil {
			s.logger.Warn("failed to save BEEF", "error", err)
		}
		return rawTx
	}

	// Raw tx — parse it
	tx, err = sdkTx.NewTransactionFromBytes(rawTx)
	if err != nil {
		s.logger.Warn("failed to parse raw tx for BEEF enrichment", "error", err)
		return rawTx
	}

	// Save to beef storage first
	if err := s.beefStorage.SaveRaw(ctx, rawTx); err != nil {
		s.logger.Warn("failed to save raw tx", "error", err)
	}

	// Populate ancestors from storage
	if err := s.beefStorage.PopulateAncestors(ctx, tx); err != nil {
		s.logger.Warn("failed to populate ancestors, submitting raw tx", "txid", tx.TxID().String(), "error", err)
		return rawTx
	}

	// Serialize as BEEF
	beefBytes, err := tx.BEEF()
	if err != nil {
		s.logger.Warn("failed to serialize BEEF, submitting raw tx", "txid", tx.TxID().String(), "error", err)
		return rawTx
	}

	s.logger.Debug("enriched raw tx with BEEF", "txid", tx.TxID().String(), "rawSize", len(rawTx), "beefSize", len(beefBytes))
	return beefBytes
}

// SubmitTransaction enriches with BEEF before delegating to the inner service.
func (s *BeefCapturingArcadeService) SubmitTransaction(ctx context.Context, rawTx []byte, opts *models.SubmitOptions) (*models.TransactionStatus, error) {
	return s.inner.SubmitTransaction(ctx, s.enrichWithBeef(ctx, rawTx), opts)
}

// SubmitTransactions enriches each tx with BEEF before delegating to the inner service.
func (s *BeefCapturingArcadeService) SubmitTransactions(ctx context.Context, rawTxs [][]byte, opts *models.SubmitOptions) ([]*models.TransactionStatus, error) {
	enriched := make([][]byte, len(rawTxs))
	for i, rawTx := range rawTxs {
		enriched[i] = s.enrichWithBeef(ctx, rawTx)
	}
	return s.inner.SubmitTransactions(ctx, enriched, opts)
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
