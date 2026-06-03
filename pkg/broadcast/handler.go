package broadcast

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/b-open-io/1sat-stack/pkg/beef"
	sdkTx "github.com/bsv-blockchain/go-sdk/transaction"
)

// Handler is the chokepoint for transaction broadcasts originating inside
// 1sat-stack. It accepts either BEEF-formatted bytes or a raw transaction,
// captures BEEF locally for indexing/ORDFS, then submits the leaf raw tx to
// arcade via the EventBroker (which uses our internal callback token, so the
// resulting status events feed our always-on SSE stream).
//
// All callers that broadcast on behalf of a 1sat-stack tenant — the /1sat/tx
// HTTP handler, paymail's receive endpoints, and any future internal
// broadcasters — go through Handler.Submit.
type Handler struct {
	broker      *arcadeclient.EventBroker
	beefStorage *beef.Storage
	logger      *slog.Logger
	waitTimeout time.Duration
}

// NewHandler constructs a Handler.
//
// broker is the always-on event broker (required). beefStorage is optional;
// if nil, BEEF capture is skipped and the input is submitted as-is.
// waitTimeout bounds how long Submit blocks before returning whatever status
// it has; pass 0 for DefaultWaitTimeout.
func NewHandler(
	broker *arcadeclient.EventBroker,
	beefStorage *beef.Storage,
	waitTimeout time.Duration,
	logger *slog.Logger,
) *Handler {
	if waitTimeout == 0 {
		waitTimeout = DefaultWaitTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		broker:      broker,
		beefStorage: beefStorage,
		logger:      logger,
		waitTimeout: waitTimeout,
	}
}

// Submit captures BEEF for the given payload and submits the leaf raw tx to
// arcade via SubmitAndWait. Payload may be either BEEF or raw transaction
// bytes; the format is auto-detected.
//
// On stop-condition reached: returns (status, nil).
// On wait timeout: returns (best-effort status, context.DeadlineExceeded).
// On submit failure: returns (nil, error).
// On capture+parse failure when payload is neither valid BEEF nor a parseable
// raw tx: returns (nil, error) without submitting.
func (h *Handler) Submit(ctx context.Context, payload []byte) (*arcadeclient.TransactionStatus, error) {
	if len(payload) == 0 {
		return nil, errors.New("empty payload")
	}
	rawTx, err := h.captureAndExtractRawTx(ctx, payload)
	if err != nil {
		h.logger.Warn("broadcast payload parse failed", "err", err, "payload_size", len(payload))
		return nil, fmt.Errorf("parse payload: %w", err)
	}
	txid, err := arcadeclient.ComputeTxid(rawTx)
	if err != nil {
		return nil, fmt.Errorf("compute txid: %w", err)
	}
	h.logger.Info("broadcast initiated",
		"txid", txid, "payload_size", len(payload), "raw_size", len(rawTx))
	status, err := h.broker.SubmitAndWait(ctx, rawTx, arcadeclient.SubmitOptions{}, arcadeclient.WaitOptions{
		Timeout: h.waitTimeout,
		StopOn:  arcadeclient.StopOnAccepted,
	})
	if status != nil {
		h.logger.Info("broadcast complete",
			"txid", status.Txid, "tx_status", status.TxStatus, "timed_out", err != nil)
	} else {
		h.logger.Warn("broadcast failed to produce status", "txid", txid, "err", err)
	}
	return status, err
}

// captureAndExtractRawTx auto-detects whether payload is BEEF or a raw tx,
// saves what's available to local BEEF storage (best effort), and returns
// the leaf raw tx bytes for submission to arcade. Storage write failures are
// logged and tolerated — they don't block submission.
func (h *Handler) captureAndExtractRawTx(ctx context.Context, payload []byte) ([]byte, error) {
	// Try BEEF first.
	if _, tx, _, err := sdkTx.ParseBeef(payload); err == nil && tx != nil {
		if h.beefStorage != nil {
			if saveErr := h.beefStorage.SaveRaw(ctx, payload); saveErr != nil {
				h.logger.Warn("failed to save BEEF", "err", saveErr)
			}
		}
		return tx.EF()
	}

	// Not BEEF — parse as a raw tx and load its ancestors so the submission
	// carries per-input source data (arcade validates scripts at intake).
	tx, err := sdkTx.NewTransactionFromBytes(payload)
	if err != nil {
		return nil, fmt.Errorf("payload is neither BEEF nor a valid transaction: %w", err)
	}

	// No storage to source ancestors from: capture disabled, submit as-is.
	if h.beefStorage == nil {
		return payload, nil
	}

	if saveErr := h.beefStorage.SaveRaw(ctx, payload); saveErr != nil {
		h.logger.Warn("failed to save raw tx", "err", saveErr)
	}
	if ancErr := h.beefStorage.PopulateAncestors(ctx, tx); ancErr != nil {
		return nil, fmt.Errorf("populate ancestors for %s: %w", tx.TxID().String(), ancErr)
	}
	if beefBytes, beefErr := tx.BEEF(); beefErr == nil {
		if saveErr := h.beefStorage.SaveRaw(ctx, beefBytes); saveErr != nil {
			h.logger.Warn("failed to save enriched BEEF", "txid", tx.TxID().String(), "err", saveErr)
		}
	}

	return tx.EF()
}
