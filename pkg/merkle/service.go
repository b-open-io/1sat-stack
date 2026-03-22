package merkle

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/redis/go-redis/v9"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
)


// ArcCallback represents an Arc transaction status update
type ArcCallback struct {
	TxID       string `json:"txId"`
	Status     string `json:"txStatus"`
	MerklePath []byte `json:"merklePath,omitempty"`
}

// Service manages merkle proof validation and transaction state transitions.
type Service struct {
	redis          *redis.Client
	beefStore      *beef.Storage
	pubsub         pubsub.PubSub
	chaintracks    chaintracker.ChainTracker
	overlayStorage engine.Storage
	logger         *slog.Logger
	immutableScore float64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewService creates a new MerkleService.
func NewService(
	r *redis.Client,
	beefStore *beef.Storage,
	ps pubsub.PubSub,
	ct chaintracker.ChainTracker,
	overlayStorage engine.Storage,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		redis:          r,
		beefStore:      beefStore,
		pubsub:         ps,
		chaintracks:    ct,
		overlayStorage: overlayStorage,
		logger:         logger,
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

	txid, err := chainhash.NewHashFromHex(callback.TxID)
	if err != nil {
		s.logger.Error("invalid txid", "txid", callback.TxID, "error", err)
		return
	}

	if s.overlayStorage != nil {
		// Find and rollback from all topics
		outputs, err := s.overlayStorage.FindOutputsForTransaction(s.ctx, txid, false)
		if err != nil {
			s.logger.Error("failed to find outputs", "txid", callback.TxID, "error", err)
			return
		}

		for _, output := range outputs {
			if output != nil {
				if err := s.overlayStorage.DeleteOutput(s.ctx, &output.Outpoint, output.Topic); err != nil {
					s.logger.Error("failed to delete output", "outpoint", output.Outpoint.String(), "error", err)
				}
			}
		}
	}

	// Log to rollback set using properly scaled timestamp
	if err := s.redis.ZAdd(s.ctx, string(txo.KeyLog(txo.RollbackTxLog)), redis.Z{
		Member: string(txid[:]),
		Score:  types.HeightScore(0, 0),
	}).Err(); err != nil {
		s.logger.Error("failed to log rollback", "txid", callback.TxID, "error", err)
	}

	// Remove from pending
	s.redis.ZRem(s.ctx, string(txo.KeyLog(txo.PendingTxLog)), string(txid[:]))
}

// SetChainTip updates the immutability threshold based on the current chain tip.
// Call this when receiving block notifications from external source.
func (s *Service) SetChainTip(height uint32) {
	s.immutableScore = types.HeightScore(height-txo.ImmutabilityBlocks, 0)
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
	beef := assembleBEEF(tx)
	if s.beefStore != nil {
		s.beefStore.SaveBeef(ctx, txid, beef)
	}

	// Update txo storage if available
	if s.overlayStorage != nil {
		s.overlayStorage.UpdateTransactionBEEF(ctx, txid, beef)
	}

	// Check if now immutable
	if newScore > 0 && newScore < s.immutableScore {
		s.logger.Debug("archiving immutable tx", "txid", txid.String())

		// Move from pending to immutable
		if err := s.redis.ZAdd(ctx, string(txo.KeyLog(txo.ImmutableTxLog)), redis.Z{
			Member: string(txid[:]),
			Score:  newScore,
		}).Err(); err != nil {
			return err
		}
		s.redis.ZRem(ctx, string(txo.KeyLog(txo.PendingTxLog)), string(txid[:]))
	}

	return nil
}

// LogPending logs a transaction as pending confirmation.
func (s *Service) LogPending(ctx context.Context, txid *chainhash.Hash, score float64) error {
	return s.redis.ZAdd(ctx, string(txo.KeyLog(txo.PendingTxLog)), redis.Z{
		Member: string(txid[:]),
		Score:  score,
	}).Err()
}

// DequeuePending removes a transaction from the pending log.
func (s *Service) DequeuePending(ctx context.Context, txid *chainhash.Hash) error {
	return s.redis.ZRem(ctx, string(txo.KeyLog(txo.PendingTxLog)), string(txid[:])).Err()
}

// GetImmutableThreshold returns the current score threshold for immutability.
func (s *Service) GetImmutableThreshold() float64 {
	return s.immutableScore
}

// assembleBEEF creates a BEEF object from a transaction.
func assembleBEEF(tx *transaction.Transaction) *transaction.Beef {
	beef := transaction.NewBeef()
	beef.BUMPs = []*transaction.MerklePath{tx.MerklePath}
	beef.Transactions[*tx.TxID()] = &transaction.BeefTx{Transaction: tx}
	return beef
}
