package indexer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
)

// StartArcBridge registers a handler on the arcade EventBroker that translates
// arcade SSE status events into the local "arc" pubsub topic that StatusHandler
// consumes. For terminal statuses it fetches the full status to populate
// MerklePath / ExtraInfo (the SSE payload is slim). No-op if broker or ps is nil.
func StartArcBridge(broker *arcadeclient.EventBroker, ps pubsub.PubSub, ac *arcadeclient.Client, logger *slog.Logger) {
	if broker == nil || ps == nil {
		return
	}
	logger.Info("registering SSE → arc pubsub bridge")
	broker.AddHandler(func(handlerCtx context.Context, evt *arcadeclient.SSEEvent) {
		arcEvent := ArcEvent{TxID: evt.Txid, Status: evt.TxStatus}
		if arcadeclient.IsTerminal(evt.TxStatus) {
			fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if status, err := ac.GetStatus(fetchCtx, evt.Txid); err == nil && status != nil {
				if path, decodeErr := hex.DecodeString(status.MerklePath); decodeErr == nil {
					arcEvent.MerklePath = path
				} else if status.MerklePath != "" {
					logger.Warn("invalid merkle path hex from arcade", "txid", evt.Txid, "err", decodeErr)
				}
				arcEvent.ExtraInfo = status.ExtraInfo
			} else if err != nil {
				logger.Warn("failed to fetch full status for terminal event", "txid", evt.Txid, "tx_status", evt.TxStatus, "err", err)
			}
			cancel()
		}
		data, err := json.Marshal(arcEvent)
		if err != nil {
			logger.Error("failed to marshal arc event", "txid", evt.Txid, "err", err)
			return
		}
		if err := ps.Publish(handlerCtx, "arc", string(data)); err != nil {
			logger.Error("failed to publish arc event", "txid", evt.Txid, "err", err)
			return
		}
		logger.Info("arc event bridged", "txid", evt.Txid, "tx_status", evt.TxStatus)
	})
}
