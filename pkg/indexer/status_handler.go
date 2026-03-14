package indexer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
)

// StatusHandler subscribes to the "arc" pubsub topic and handles all transaction status updates.
// This consolidates ingestion, proof validation, and rollback into one handler.
type StatusHandler struct {
	pubsub         pubsub.PubSub
	store          store.Store
	beefStorage    *beef.Storage
	overlayStorage engine.Storage
	chainTracker   chaintracker.ChainTracker
	indexer        *IngestCtx
	logger         *slog.Logger

	ingestEnabled  bool
	immutableScore float64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// StatusHandlerConfig holds configuration for the status handler.
type StatusHandlerConfig struct {
	IngestEnabled bool // Ingest transactions on ACCEPTED status
}

// NewStatusHandler creates a new status handler.
func NewStatusHandler(
	ps pubsub.PubSub,
	s store.Store,
	beefStorage *beef.Storage,
	overlayStorage engine.Storage,
	ct chaintracker.ChainTracker,
	indexer *IngestCtx,
	cfg *StatusHandlerConfig,
	logger *slog.Logger,
) *StatusHandler {
	if logger == nil {
		logger = slog.Default()
	}

	ingestEnabled := true
	if cfg != nil {
		ingestEnabled = cfg.IngestEnabled
	}

	return &StatusHandler{
		pubsub:         ps,
		store:          s,
		beefStorage:    beefStorage,
		overlayStorage: overlayStorage,
		chainTracker:   ct,
		indexer:        indexer,
		ingestEnabled:  ingestEnabled,
		logger:         logger,
	}
}

// Start begins listening for arc events.
func (h *StatusHandler) Start(ctx context.Context) error {
	if h.pubsub == nil {
		h.logger.Warn("no pubsub configured, status handler disabled")
		return nil
	}

	h.ctx, h.cancel = context.WithCancel(ctx)

	h.wg.Add(1)
	go h.listen()

	h.logger.Info("status handler started", "ingest", h.ingestEnabled)
	return nil
}

// Stop stops the handler gracefully.
func (h *StatusHandler) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	h.wg.Wait()
	h.logger.Info("status handler stopped")
}

// SetChainTip updates the immutability threshold based on the current chain tip.
func (h *StatusHandler) SetChainTip(height uint32) {
	h.immutableScore = types.HeightScore(height-txo.ImmutabilityBlocks, 0)
	h.logger.Info("chain tip updated", "height", height, "immutable_threshold", h.immutableScore)
}

// listen subscribes to the "arc" topic and processes events.
func (h *StatusHandler) listen() {
	defer h.wg.Done()

	eventCh, err := h.pubsub.Subscribe(h.ctx, []string{"arc"})
	if err != nil {
		h.logger.Error("failed to subscribe to arc topic", "error", err)
		return
	}

	h.logger.Info("arc topic listener started")

	for {
		select {
		case <-h.ctx.Done():
			h.logger.Info("status handler shutting down")
			return
		case event, ok := <-eventCh:
			if !ok {
				h.logger.Info("arc event channel closed")
				return
			}
			h.handleEvent(event)
		}
	}
}

// handleEvent processes a single arc event.
func (h *StatusHandler) handleEvent(event pubsub.Event) {
	var arcEvent ArcEvent
	if err := json.Unmarshal([]byte(event.Member), &arcEvent); err != nil {
		h.logger.Error("failed to parse arc event", "error", err)
		return
	}

	if arcEvent.TxID == "" || arcEvent.Status == "" {
		return
	}

	h.logger.Info("arc event received",
		"txid", arcEvent.TxID,
		"status", arcEvent.Status)

	switch arcEvent.Status {
	case "ACCEPTED_BY_NETWORK", "SENT_TO_NETWORK":
		if h.ingestEnabled {
			go h.handleAccepted(arcEvent)
		}

	case "MINED":
		go h.handleMined(arcEvent)

	case "REJECTED", "DOUBLE_SPEND_ATTEMPTED":
		go h.handleRejected(arcEvent)
	}
}

// handleAccepted ingests a newly accepted transaction.
func (h *StatusHandler) handleAccepted(event ArcEvent) {
	h.logger.Info("handleAccepted called", "txid", event.TxID)

	if h.indexer == nil || h.beefStorage == nil {
		h.logger.Warn("handleAccepted skipped - missing indexer or beefStorage",
			"txid", event.TxID,
			"hasIndexer", h.indexer != nil,
			"hasBeefStorage", h.beefStorage != nil)
		return
	}

	txid, err := chainhash.NewHashFromHex(event.TxID)
	if err != nil {
		h.logger.Error("invalid txid", "txid", event.TxID, "error", err)
		return
	}

	// Try to load from beef storage
	tx, err := h.beefStorage.LoadTx(h.ctx, txid)
	if err != nil || tx == nil {
		h.logger.Info("tx not in beef storage yet", "txid", event.TxID, "error", err)
		return
	}

	// Ingest the transaction (IngestTx handles tx:pending logging)
	if _, err := h.indexer.IngestTx(h.ctx, tx); err != nil {
		h.logger.Error("failed to ingest transaction", "txid", event.TxID, "error", err)
		return
	}

	h.logger.Info("transaction ingested", "txid", event.TxID)
}

// handleMined updates storage with merkle proof from a mined transaction.
func (h *StatusHandler) handleMined(event ArcEvent) {
	if h.beefStorage == nil {
		return
	}

	if len(event.MerklePath) == 0 {
		h.logger.Debug("no merkle path in mined event", "txid", event.TxID)
		return
	}

	txid, err := chainhash.NewHashFromHex(event.TxID)
	if err != nil {
		h.logger.Error("invalid txid", "txid", event.TxID, "error", err)
		return
	}

	// Parse merkle path
	merklePath, err := transaction.NewMerklePathFromBinary(event.MerklePath)
	if err != nil {
		h.logger.Error("failed to parse merkle path", "txid", event.TxID, "error", err)
		return
	}

	// Validate against chain tracker if available
	if h.chainTracker != nil {
		root, err := merklePath.ComputeRoot(txid)
		if err != nil {
			h.logger.Error("failed to compute merkle root", "txid", event.TxID, "error", err)
			return
		}

		valid, err := h.chainTracker.IsValidRootForHeight(h.ctx, root, merklePath.BlockHeight)
		if err != nil {
			h.logger.Error("failed to validate merkle root", "txid", event.TxID, "error", err)
			return
		}
		if !valid {
			h.logger.Warn("invalid merkle proof", "txid", event.TxID, "height", merklePath.BlockHeight)
			return
		}
	}

	// Calculate score from merkle path
	var newScore float64
	for _, path := range merklePath.Path[0] {
		if txid.IsEqual(path.Hash) {
			newScore = types.HeightScore(merklePath.BlockHeight, path.Offset)
			break
		}
	}

	if newScore == 0 {
		h.logger.Warn("transaction not in proof", "txid", event.TxID)
		return
	}

	h.logger.Debug("updating tx with merkle proof",
		"txid", event.TxID,
		"height", merklePath.BlockHeight,
		"score", newScore)

	// Load transaction and attach merkle path
	tx, err := h.beefStorage.LoadTx(h.ctx, txid)
	if err != nil {
		h.logger.Warn("could not load tx for BEEF update", "txid", event.TxID, "error", err)
		return
	}

	if tx != nil {
		tx.MerklePath = merklePath
		beef := assembleBEEF(tx)
		if beef != nil {
			h.beefStorage.SaveBeef(h.ctx, txid, beef)

			// Also update TXO storage if available
			if h.overlayStorage != nil {
				h.overlayStorage.UpdateTransactionBEEF(h.ctx, txid, beef)
			}
		}

		// Re-ingest to update scores with confirmed block height
		if h.indexer != nil {
			if _, err := h.indexer.IngestTx(h.ctx, tx); err != nil {
				h.logger.Error("failed to re-ingest mined tx", "txid", event.TxID, "error", err)
			}
		}
	}

	h.logger.Info("transaction mined", "txid", event.TxID, "height", merklePath.BlockHeight)
}

// handleRejected rolls back outputs for a rejected transaction.
func (h *StatusHandler) handleRejected(event ArcEvent) {
	// Log raw tx hex for debugging if available
	if event.RawTx != "" {
		h.logger.Debug("rejected transaction raw hex", "txid", event.TxID, "rawTx", event.RawTx)
	} else if h.beefStorage != nil {
		// Try to load and log raw tx
		txid, err := chainhash.NewHashFromHex(event.TxID)
		if err == nil {
			if tx, err := h.beefStorage.LoadTx(h.ctx, txid); err == nil && tx != nil {
				h.logger.Debug("rejected transaction raw hex", "txid", event.TxID, "rawTx", hex.EncodeToString(tx.Bytes()))
			}
		}
	}

	h.logger.Info("transaction rejected", "txid", event.TxID, "reason", event.ExtraInfo)

	if h.overlayStorage == nil {
		return
	}

	txid, err := chainhash.NewHashFromHex(event.TxID)
	if err != nil {
		h.logger.Error("invalid txid", "txid", event.TxID, "error", err)
		return
	}

	// Find and delete outputs from all topics
	outputs, err := h.overlayStorage.FindOutputsForTransaction(h.ctx, txid, false)
	if err != nil {
		h.logger.Error("failed to find outputs for rollback", "txid", event.TxID, "error", err)
		return
	}

	for _, output := range outputs {
		if output != nil {
			if err := h.overlayStorage.DeleteOutput(h.ctx, &output.Outpoint, output.Topic); err != nil {
				h.logger.Error("failed to delete output", "outpoint", output.Outpoint.String(), "error", err)
			}
		}
	}

	// Log to rollback set and remove from pending
	if h.store != nil {
		if err := h.store.ZAdd(h.ctx, txo.KeyLog(txo.RollbackTxLog), store.ScoredMember{
			Member: txid[:],
			Score:  types.HeightScore(0, 0),
		}); err != nil {
			h.logger.Error("failed to log rollback", "txid", event.TxID, "error", err)
		}
		h.store.ZRem(h.ctx, txo.KeyLog(txo.PendingTxLog), txid[:])
	}

	h.logger.Info("rolled back rejected tx", "txid", event.TxID, "outputs", len(outputs))
}

// LogPending logs a transaction as pending confirmation.
func (h *StatusHandler) LogPending(ctx context.Context, txid *chainhash.Hash, score float64) error {
	if h.store == nil {
		return nil
	}
	return h.store.ZAdd(ctx, txo.KeyLog(txo.PendingTxLog), store.ScoredMember{
		Member: txid[:],
		Score:  score,
	})
}

// DequeuePending removes a transaction from the pending log.
func (h *StatusHandler) DequeuePending(ctx context.Context, txid *chainhash.Hash) error {
	if h.store == nil {
		return nil
	}
	return h.store.ZRem(ctx, txo.KeyLog(txo.PendingTxLog), txid[:])
}

// GetImmutableThreshold returns the current score threshold for immutability.
func (h *StatusHandler) GetImmutableThreshold() float64 {
	return h.immutableScore
}

// assembleBEEF creates a BEEF object from a transaction with merkle path.
func assembleBEEF(tx *transaction.Transaction) *transaction.Beef {
	if tx.MerklePath == nil {
		return nil
	}
	beef := transaction.NewBeef()
	beef.BUMPs = []*transaction.MerklePath{tx.MerklePath}
	beef.Transactions[*tx.TxID()] = &transaction.BeefTx{Transaction: tx}
	return beef
}
