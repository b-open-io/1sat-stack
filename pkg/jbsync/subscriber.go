package jbsync

import (
	"context"
	"encoding/binary"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/b-open-io/go-junglebus"
	"github.com/b-open-io/go-junglebus/models"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-sdk/chainhash"
)

// Subscriber manages a JungleBus subscription and writes transactions to a queue
type Subscriber struct {
	config       *SubscriberConfig
	store        store.Store
	chainTracker chaintracks.Chaintracks
	jbClient     *junglebus.Client
	logger       *slog.Logger
}

// NewSubscriber creates a new JungleBus subscriber
func NewSubscriber(cfg *SubscriberConfig, s store.Store, ct chaintracks.Chaintracks, jbClient *junglebus.Client, logger *slog.Logger) (*Subscriber, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Set defaults
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	if cfg.ReorgDepth == 0 {
		cfg.ReorgDepth = 6
	}

	return &Subscriber{
		config:       cfg,
		store:        s,
		chainTracker: ct,
		jbClient:     jbClient,
		logger:       logger.With("component", "jbsync", "subscription", cfg.SubscriptionID),
	}, nil
}

// Start begins the JungleBus subscription (blocking)
func (s *Subscriber) Start(ctx context.Context) error {
	queueKey := []byte(QueueKey(s.config.GetQueueName()))

	// Check for existing progress
	startBlock := s.config.FromBlock
	if progressBytes, err := s.store.HGet(ctx, txo.KeyProgress, []byte(s.config.SubscriptionID)); err == nil && len(progressBytes) == 4 {
		startBlock = uint64(binary.BigEndian.Uint32(progressBytes))
		s.logger.Info("resuming subscription", "from_block", startBlock)
	}

	txcount := 0
	s.logger.Info("starting JungleBus subscription", "from_block", startBlock)

	// Create error channel for database errors from callbacks
	errChan := make(chan error, 1)

	// Batch management
	var batchMembers []store.ScoredMember
	maxBatchSize := s.config.BatchSize

	// Helper function to flush batch
	flushBatch := func() error {
		if len(batchMembers) == 0 {
			return nil
		}

		if err := s.store.ZAdd(ctx, queueKey, batchMembers...); err != nil {
			s.logger.Error("failed to add batch to queue", "error", err, "batch_size", len(batchMembers))
			return err
		}

		batchMembers = batchMembers[:0]
		return nil
	}

	// Create event handler
	handler := junglebus.EventHandler{
		OnTransaction: func(txn *models.TransactionResponse) {
			txcount++
			s.logger.Debug("processing transaction",
				"block_height", txn.BlockHeight,
				"block_index", txn.BlockIndex,
				"txid", txn.Id)

			// Parse txid to binary hash
			txid, err := chainhash.NewHashFromHex(txn.Id)
			if err != nil {
				s.logger.Error("failed to parse txid", "error", err, "txid", txn.Id)
				return
			}

			// Calculate score using unified HeightScore
			score := types.HeightScore(txn.BlockHeight, txn.BlockIndex)

			// Add to batch (binary 32-byte txid)
			batchMembers = append(batchMembers, store.ScoredMember{
				Member: txid[:],
				Score:  score,
			})

			// Flush if batch is full
			if len(batchMembers) >= maxBatchSize {
				if err := flushBatch(); err != nil {
					select {
					case errChan <- err:
					default:
					}
				}
			}
		},
		OnStatus: func(status *models.ControlResponse) {
			s.logger.Debug("subscription status", "status_code", status.StatusCode, "block", status.Block, "message", status.Message)

			switch status.StatusCode {
			case 200: // Block done
				// Flush any remaining transactions
				if err := flushBatch(); err != nil {
					select {
					case errChan <- err:
					default:
					}
					return
				}

				// Update progress with reorg protection
				progressHeight := status.Block + 1
				var currentTip uint32
				if s.chainTracker != nil {
					currentTip = s.chainTracker.GetHeight(ctx)
				}

				finalSafeHeight := progressHeight
				if currentTip > s.config.ReorgDepth {
					minSafeHeight := currentTip - s.config.ReorgDepth
					if finalSafeHeight > minSafeHeight {
						finalSafeHeight = minSafeHeight
					}
				}
				if uint64(finalSafeHeight) < startBlock {
					finalSafeHeight = uint32(startBlock)
				}

				// Check for context cancellation
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Only update if moving forward
				if uint64(finalSafeHeight) <= startBlock {
					s.logger.Debug("skipping backward progress update",
						"last_written", startBlock,
						"attempted", finalSafeHeight)
					return
				}

				// Update progress
				progressBytes := make([]byte, 4)
				binary.BigEndian.PutUint32(progressBytes, finalSafeHeight)
				if err := s.store.HSet(ctx, txo.KeyProgress, []byte(s.config.SubscriptionID), progressBytes); err != nil {
					if ctx.Err() == nil {
						s.logger.Error("failed to update progress", "error", err)
						select {
						case errChan <- err:
						default:
						}
					}
				}
				startBlock = uint64(finalSafeHeight)

				s.logger.Info("block synced",
					"block", status.Block,
					"txs", txcount,
					"progress", finalSafeHeight)
				txcount = 0

			case 999: // Subscription completed/error
				s.logger.Info("subscription completed", "message", status.Message)
			}
		},
		OnError: func(err error) {
			s.logger.Error("JungleBus subscription error", "error", err)
		},
	}

	// Add mempool handler if enabled
	if s.config.EnableMempool {
		handler.OnMempool = func(txn *models.TransactionResponse) {
			// Parse txid to binary hash
			txid, err := chainhash.NewHashFromHex(txn.Id)
			if err != nil {
				s.logger.Error("failed to parse mempool txid", "error", err, "txid", txn.Id)
				return
			}

			// Mempool transactions use timestamp-based score via HeightScore(0, 0)
			score := types.HeightScore(0, 0)

			if err := s.store.ZAdd(ctx, queueKey, store.ScoredMember{
				Member: txid[:],
				Score:  score,
			}); err != nil {
				s.logger.Error("failed to add mempool tx to queue", "error", err, "txid", txn.Id)
			}
		}
	}

	// Create subscription
	sub, err := s.jbClient.SubscribeWithQueue(ctx,
		s.config.SubscriptionID,
		startBlock,
		0, // fromPage
		handler,
		&junglebus.SubscribeOptions{
			QueueSize: 1000,
			LiteMode:  true, // We only need txids
		},
	)
	if err != nil {
		return err
	}

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		if sub != nil {
			sub.Unsubscribe()
		}
		return nil
	case err := <-errChan:
		if sub != nil {
			sub.Unsubscribe()
		}
		return err
	}
}

// GetProgress returns the current sync progress for this subscription
func (s *Subscriber) GetProgress(ctx context.Context) (uint64, error) {
	progressBytes, err := s.store.HGet(ctx, txo.KeyProgress, []byte(s.config.SubscriptionID))
	if err != nil || len(progressBytes) != 4 {
		return s.config.FromBlock, nil
	}
	return uint64(binary.BigEndian.Uint32(progressBytes)), nil
}
