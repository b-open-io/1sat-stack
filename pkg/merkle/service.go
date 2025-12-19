package merkle

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
)

// Log keys for transaction tracking
const (
	PendingTxLog   = "tx:pending"   // Transactions awaiting confirmation
	ImmutableTxLog = "tx:immutable" // Confirmed transactions (>10 blocks)
	RollbackTxLog  = "tx:rollback"  // Rolled back transactions
)

// ImmutabilityBlocks is the number of confirmations before a tx is considered immutable
const ImmutabilityBlocks = 10

// ArcCallback represents an Arc transaction status update
type ArcCallback struct {
	TxID       string `json:"txId"`
	Status     string `json:"txStatus"`
	MerklePath []byte `json:"merklePath,omitempty"`
}

// Service manages merkle proof validation and transaction state transitions.
type Service struct {
	store          store.Store
	beefStore      *beef.Storage
	pubsub         pubsub.PubSub
	chaintracks    chaintracker.ChainTracker
	txoStore       *txo.OutputStore
	logger         *slog.Logger
	immutableScore float64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewService creates a new MerkleService.
func NewService(
	s store.Store,
	beefStore *beef.Storage,
	ps pubsub.PubSub,
	ct chaintracker.ChainTracker,
	txoStore *txo.OutputStore,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		store:       s,
		beefStore:   beefStore,
		pubsub:      ps,
		chaintracks: ct,
		txoStore:    txoStore,
		logger:      logger,
	}
}

// Start begins listening for Arc callbacks.
func (s *Service) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Start Arc callback listener
	s.wg.Add(1)
	go s.listenArcCallbacks()

	return nil
}

// Stop stops the service gracefully.
func (s *Service) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return nil
}

// listenArcCallbacks listens for Arc transaction status updates.
func (s *Service) listenArcCallbacks() {
	defer s.wg.Done()

	if s.pubsub == nil {
		s.logger.Warn("pubsub not configured, Arc callbacks disabled")
		return
	}

	eventChan, err := s.pubsub.Subscribe(s.ctx, []string{"arc"})
	if err != nil {
		s.logger.Error("failed to subscribe to arc topic", "error", err)
		return
	}

	s.logger.Info("Arc callback listener started")

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				s.logger.Info("Arc callback channel closed")
				return
			}

			var callback ArcCallback
			if err := json.Unmarshal([]byte(event.Member), &callback); err != nil {
				s.logger.Error("failed to parse Arc callback", "error", err)
				continue
			}

			if callback.TxID == "" || callback.Status == "" {
				continue
			}

			s.logger.Debug("Arc callback received",
				"txid", callback.TxID,
				"status", callback.Status,
			)

			// Handle different callback scenarios
			switch {
			case len(callback.MerklePath) > 0:
				// Transaction mined with proof
				go s.handleMinedCallback(callback)

			case callback.Status == "REJECTED" || callback.Status == "DOUBLE_SPEND_ATTEMPTED":
				// Transaction rejected
				go s.handleRejectedCallback(callback)
			}

		case <-s.ctx.Done():
			s.logger.Info("Arc callback listener shutting down")
			return
		}
	}
}

// handleMinedCallback processes a mined transaction with merkle proof.
func (s *Service) handleMinedCallback(callback ArcCallback) {
	txid, err := chainhash.NewHashFromHex(callback.TxID)
	if err != nil {
		s.logger.Error("invalid txid in Arc callback", "txid", callback.TxID, "error", err)
		return
	}

	// Load transaction
	tx, err := s.beefStore.LoadTx(s.ctx, txid)
	if err != nil {
		s.logger.Error("failed to load tx", "txid", callback.TxID, "error", err)
		return
	}

	// Parse merkle path
	tx.MerklePath, err = transaction.NewMerklePathFromBinary(callback.MerklePath)
	if err != nil {
		s.logger.Error("failed to parse merkle path", "txid", callback.TxID, "error", err)
		return
	}

	// Validate and update
	if err := s.ValidateAndUpdateTx(s.ctx, txid, tx); err != nil {
		s.logger.Error("failed to update tx", "txid", callback.TxID, "error", err)
	}
}

// handleRejectedCallback processes a rejected transaction.
func (s *Service) handleRejectedCallback(callback ArcCallback) {
	s.logger.Info("rolling back rejected tx", "txid", callback.TxID)

	if s.txoStore != nil {
		txid, err := chainhash.NewHashFromHex(callback.TxID)
		if err != nil {
			s.logger.Error("invalid txid", "txid", callback.TxID, "error", err)
			return
		}

		// Find and rollback from all topics
		outputs, err := s.txoStore.FindOutputsForTransaction(s.ctx, txid, false)
		if err != nil {
			s.logger.Error("failed to find outputs", "txid", callback.TxID, "error", err)
			return
		}

		for _, output := range outputs {
			if output != nil {
				if err := s.txoStore.DeleteOutput(s.ctx, &output.Outpoint, output.Topic); err != nil {
					s.logger.Error("failed to delete output", "outpoint", output.Outpoint.String(), "error", err)
				}
			}
		}
	}

	// Log to rollback set
	if err := s.store.ZAdd(s.ctx, []byte(RollbackTxLog), store.ScoredMember{
		Member: []byte(callback.TxID),
		Score:  float64(time.Now().UnixNano()),
	}); err != nil {
		s.logger.Error("failed to log rollback", "txid", callback.TxID, "error", err)
	}

	// Remove from pending
	s.store.ZRem(s.ctx, []byte(PendingTxLog), []byte(callback.TxID))
}

// SetChainTip updates the immutability threshold based on the current chain tip.
// Call this when receiving block notifications from external source.
func (s *Service) SetChainTip(height uint32) {
	s.immutableScore = types.HeightScore(height-ImmutabilityBlocks, 0)
	s.logger.Info("chain tip updated", "height", height, "immutable_threshold", s.immutableScore)
}

// ValidateAndUpdateTx validates a merkle proof and updates transaction scores.
func (s *Service) ValidateAndUpdateTx(ctx context.Context, txid *chainhash.Hash, tx *transaction.Transaction) error {
	if tx.MerklePath == nil {
		return nil
	}

	// Compute merkle root
	root, err := tx.MerklePath.ComputeRoot(txid)
	if err != nil {
		s.logger.Error("ComputeRoot error", "txid", txid.String(), "error", err)
		return err
	}

	// Validate against chain
	if s.chaintracks != nil {
		valid, err := s.chaintracks.IsValidRootForHeight(ctx, root, tx.MerklePath.BlockHeight)
		if err != nil {
			s.logger.Error("IsValidRootForHeight error", "txid", txid.String(), "error", err)
			return err
		}
		if !valid {
			s.logger.Warn("invalid proof", "txid", txid.String())
			return nil
		}
	}

	// Calculate new score from merkle path
	var newScore float64
	for _, path := range tx.MerklePath.Path[0] {
		if txid.IsEqual(path.Hash) {
			newScore = types.HeightScore(tx.MerklePath.BlockHeight, path.Offset)
			break
		}
	}

	if newScore == 0 {
		s.logger.Warn("transaction not in proof", "txid", txid.String())
		return nil
	}

	s.logger.Debug("updating tx score", "txid", txid.String(), "score", newScore)

	// Update BEEF storage with new proof
	if s.beefStore != nil {
		beefBytes, err := assembleBEEF(tx)
		if err == nil {
			s.beefStore.SaveBeef(ctx, txid, beefBytes)
		}
	}

	// Update txo storage if available
	if s.txoStore != nil {
		beefBytes, _ := assembleBEEF(tx)
		if len(beefBytes) > 0 {
			s.txoStore.UpdateTransactionBEEF(ctx, txid, beefBytes)
		}
	}

	// Check if now immutable
	if newScore > 0 && newScore < s.immutableScore {
		s.logger.Debug("archiving immutable tx", "txid", txid.String())

		// Move from pending to immutable
		if err := s.store.ZAdd(ctx, []byte(ImmutableTxLog), store.ScoredMember{
			Member: []byte(txid.String()),
			Score:  newScore,
		}); err != nil {
			return err
		}
		s.store.ZRem(ctx, []byte(PendingTxLog), []byte(txid.String()))
	}

	return nil
}

// LogPending logs a transaction as pending confirmation.
func (s *Service) LogPending(ctx context.Context, txid string, score float64) error {
	return s.store.ZAdd(ctx, []byte(PendingTxLog), store.ScoredMember{
		Member: []byte(txid),
		Score:  score,
	})
}

// DequeuePending removes a transaction from the pending log.
func (s *Service) DequeuePending(ctx context.Context, txid string) error {
	return s.store.ZRem(ctx, []byte(PendingTxLog), []byte(txid))
}

// GetImmutableThreshold returns the current score threshold for immutability.
func (s *Service) GetImmutableThreshold() float64 {
	return s.immutableScore
}

// assembleBEEF creates BEEF bytes from a transaction.
func assembleBEEF(tx *transaction.Transaction) ([]byte, error) {
	beef := transaction.NewBeef()
	beef.BUMPs = []*transaction.MerklePath{tx.MerklePath}
	beef.Transactions[*tx.TxID()] = &transaction.BeefTx{Transaction: tx}
	return beef.Bytes()
}
