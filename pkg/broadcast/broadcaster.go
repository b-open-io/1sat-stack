package broadcast

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Broadcaster implements go-sdk's transaction.Broadcaster against arcade.
//
// Submission goes through arcadeclient.Submit with the configured callback
// token, so the resulting tx surfaces on the always-on SSE stream like every
// other broadcast originated by 1sat-stack. Used by the overlay engine for
// transaction propagation triggered during topic admission.
//
// Fire-and-forget at this layer: the engine doesn't expect to wait for
// terminal status. Status updates flow back to 1sat-stack through the
// EventBroker's always-on handlers.
type Broadcaster struct {
	client *arcadeclient.Client
	logger *slog.Logger
}

// NewBroadcaster constructs a Broadcaster.
func NewBroadcaster(client *arcadeclient.Client, logger *slog.Logger) *Broadcaster {
	if logger == nil {
		logger = slog.Default()
	}
	return &Broadcaster{client: client, logger: logger}
}

// Broadcast submits the transaction without an explicit context.
func (b *Broadcaster) Broadcast(tx *transaction.Transaction) (*transaction.BroadcastSuccess, *transaction.BroadcastFailure) {
	return b.BroadcastCtx(context.Background(), tx)
}

// BroadcastCtx submits the transaction and returns immediately on the
// arcade-acknowledged 202. The caller does not wait for terminal status;
// downstream subscribers on the SSE stream observe lifecycle updates.
func (b *Broadcaster) BroadcastCtx(ctx context.Context, tx *transaction.Transaction) (*transaction.BroadcastSuccess, *transaction.BroadcastFailure) {
	rawTx, err := tx.EF()
	if err != nil {
		b.logger.Warn("overlay broadcast: extended-format serialization failed", "txid", tx.TxID().String(), "err", err)
		return nil, &transaction.BroadcastFailure{
			Code:        "ef_error",
			Description: err.Error(),
		}
	}
	txid, _, err := b.client.Submit(ctx, rawTx, arcadeclient.SubmitOptions{})
	if err != nil {
		b.logger.Warn("overlay broadcast failed", "err", err, "raw_size", len(rawTx))
		return nil, &transaction.BroadcastFailure{
			Code:        "submit_error",
			Description: err.Error(),
		}
	}
	b.logger.Info("overlay broadcast submitted", "txid", txid, "raw_size", len(rawTx))
	return &transaction.BroadcastSuccess{
		Txid:    txid,
		Message: "submitted",
	}, nil
}
