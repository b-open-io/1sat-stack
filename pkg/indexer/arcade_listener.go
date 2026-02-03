package indexer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/bsv-blockchain/arcade/events"
	"github.com/bsv-blockchain/arcade/models"
)

// ArcadeListener subscribes to arcade's EventPublisher and bridges events to the pubsub "arc" topic.
// This allows all arcade events (from embedded mode) to flow through the same channel as webhook callbacks.
type ArcadeListener struct {
	eventPublisher events.Publisher
	pubsub         pubsub.PubSub
	logger         *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewArcadeListener creates a new arcade event listener that bridges to pubsub.
func NewArcadeListener(
	eventPublisher events.Publisher,
	ps pubsub.PubSub,
	logger *slog.Logger,
) *ArcadeListener {
	if logger == nil {
		logger = slog.Default()
	}

	return &ArcadeListener{
		eventPublisher: eventPublisher,
		pubsub:         ps,
		logger:         logger,
	}
}

// Start begins listening for arcade events and publishing to pubsub.
func (l *ArcadeListener) Start(ctx context.Context) error {
	if l.eventPublisher == nil {
		l.logger.Warn("no event publisher configured, arcade listener disabled")
		return nil
	}

	if l.pubsub == nil {
		l.logger.Warn("no pubsub configured, arcade listener disabled")
		return nil
	}

	l.ctx, l.cancel = context.WithCancel(ctx)

	l.wg.Add(1)
	go l.listen()

	l.logger.Info("arcade listener started (bridging to pubsub)")
	return nil
}

// Stop stops the listener gracefully.
func (l *ArcadeListener) Stop() {
	if l.cancel != nil {
		l.cancel()
	}
	l.wg.Wait()
	l.logger.Info("arcade listener stopped")
}

// listen subscribes to arcade events and republishes them to the "arc" pubsub topic.
func (l *ArcadeListener) listen() {
	defer l.wg.Done()

	l.logger.Info("arcade listener subscribing to event publisher")
	eventCh, err := l.eventPublisher.Subscribe(l.ctx)
	if err != nil {
		l.logger.Error("failed to subscribe to arcade events", "error", err)
		return
	}
	l.logger.Info("arcade listener subscribed, waiting for events")

	for {
		select {
		case <-l.ctx.Done():
			l.logger.Info("arcade listener context done")
			return
		case status, ok := <-eventCh:
			if !ok {
				l.logger.Info("arcade event channel closed")
				return
			}
			l.logger.Info("arcade listener received event", "txid", status.TxID, "status", status.Status)
			l.bridgeToPublish(status)
		}
	}
}

// ArcEvent is the unified event format published to the "arc" topic.
// This matches the format expected by the status handler.
type ArcEvent struct {
	TxID       string `json:"txId"`
	Status     string `json:"txStatus"`
	MerklePath []byte `json:"merklePath,omitempty"`
	ExtraInfo  string `json:"extraInfo,omitempty"`
	RawTx      string `json:"rawTx,omitempty"` // Hex-encoded raw tx (non-EF format)
}

// bridgeToPublish converts an arcade TransactionStatus to ArcEvent and publishes to pubsub.
func (l *ArcadeListener) bridgeToPublish(status *models.TransactionStatus) {
	if status == nil || status.TxID == "" {
		return
	}

	l.logger.Info("bridging arcade event to pubsub",
		"txid", status.TxID,
		"status", status.Status)

	event := ArcEvent{
		TxID:       status.TxID,
		Status:     string(status.Status),
		MerklePath: status.MerklePath,
		ExtraInfo:  status.ExtraInfo,
	}

	data, err := json.Marshal(event)
	if err != nil {
		l.logger.Error("failed to marshal arc event", "txid", status.TxID, "error", err)
		return
	}

	if err := l.pubsub.Publish(l.ctx, "arc", string(data)); err != nil {
		l.logger.Error("failed to publish arc event", "txid", status.TxID, "error", err)
	}
}

// BridgeRawTx publishes an arc event with the raw transaction hex for debugging rejected txs.
func (l *ArcadeListener) BridgeRawTx(ctx context.Context, txid string, status string, rawTx []byte, extraInfo string) error {
	event := ArcEvent{
		TxID:      txid,
		Status:    status,
		ExtraInfo: extraInfo,
		RawTx:     hex.EncodeToString(rawTx),
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return l.pubsub.Publish(ctx, "arc", string(data))
}
