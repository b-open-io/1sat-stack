// Package broadcast exposes 1sat-stack's transaction broadcast HTTP surface
// (POST /1sat/tx, GET /1sat/tx/:txid). The handlers internally use the
// arcadeclient EventBroker to submit to arcade, wait for status updates over
// SSE, and return a synchronous TransactionStatus to the caller.
//
// External callers do not get to register their own callback URLs/tokens;
// that's a 1sat-stack-internal concern and headers are stripped accordingly.
package broadcast

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/gofiber/fiber/v2"
)

// DefaultWaitTimeout is the wait-window for a /1sat/tx submission when the
// caller doesn't override via configuration.
const DefaultWaitTimeout = 30 * time.Second

// Routes provides the broadcast HTTP surface.
type Routes struct {
	handler *Handler
	client  *arcadeclient.Client
	logger  *slog.Logger
}

// NewRoutes constructs a Routes instance.
//
// handler does BEEF capture and the SubmitAndWait dance against arcade.
// client is used by the status handler for direct GetStatus passthrough.
func NewRoutes(handler *Handler, client *arcadeclient.Client, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{handler: handler, client: client, logger: logger}
}

// Register mounts the broadcast routes on the given Fiber router.
// Typical usage: api.Group("/tx") under the existing "/1sat" base path.
func (r *Routes) Register(router fiber.Router) {
	router.Post("/", r.handleSubmit)
	router.Get("/:txid", r.handleGetStatus)
}

// handleSubmit handles POST /1sat/tx.
//
// Accepts the raw tx in three formats, matched on Content-Type:
//   - application/octet-stream — raw tx bytes (default)
//   - text/plain                — hex-encoded tx
//   - application/json          — { "rawTx": "<hex>" }
//
// Submits to arcade with our internal callback token, waits up to waitTimeout
// for the tx to reach an "accepted" tier or terminal status, then returns the
// full TransactionStatus.
//
// Status code mapping:
//   - 200 — accepted/terminal-success
//   - 202 — wait timed out; tx is in flight, status is best-effort
//   - 400 — REJECTED or DOUBLE_SPEND_ATTEMPTED (or malformed body)
//   - 502 — upstream arcade error
func (r *Routes) handleSubmit(c *fiber.Ctx) error {
	payload, err := readPayload(c)
	if err != nil {
		r.logger.Warn("/1sat/tx payload read failed", "err", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if len(payload) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "empty body"})
	}

	r.logger.Info("/1sat/tx received", "size", len(payload), "content_type", c.Get(fiber.HeaderContentType))
	status, err := r.handler.Submit(c.UserContext(), payload)

	switch {
	case errors.Is(err, context.Canceled):
		// Client disconnected; nothing to write.
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return c.Status(fiber.StatusAccepted).JSON(status)
	case err != nil:
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	if status == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "nil status"})
	}
	if arcadeclient.IsRejected(status.TxStatus) {
		return c.Status(fiber.StatusBadRequest).JSON(status)
	}
	return c.Status(fiber.StatusOK).JSON(status)
}

// handleGetStatus handles GET /1sat/tx/:txid — direct passthrough to arcade.
func (r *Routes) handleGetStatus(c *fiber.Ctx) error {
	txid := strings.TrimSpace(c.Params("txid"))
	if txid == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "txid required"})
	}

	status, err := r.client.GetStatus(c.UserContext(), txid)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	if status == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "unknown txid"})
	}
	return c.Status(fiber.StatusOK).JSON(status)
}

// readPayload extracts request bytes (BEEF or raw tx — Handler auto-detects)
// from a Fiber request based on Content-Type.
func readPayload(c *fiber.Ctx) ([]byte, error) {
	body := c.Body()
	if len(body) == 0 {
		return nil, nil
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(c.Get(fiber.HeaderContentType), ";", 2)[0]))
	switch ct {
	case "", "application/octet-stream":
		return body, nil
	case "text/plain":
		decoded, err := hex.DecodeString(strings.TrimSpace(string(body)))
		if err != nil {
			return nil, errors.New("invalid hex body")
		}
		return decoded, nil
	case "application/json":
		var payload struct {
			RawTx string `json:"rawTx"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, errors.New("invalid JSON body")
		}
		decoded, err := hex.DecodeString(strings.TrimSpace(payload.RawTx))
		if err != nil {
			return nil, errors.New("invalid rawTx hex")
		}
		return decoded, nil
	default:
		return nil, errors.New("unsupported Content-Type; expected application/octet-stream, text/plain, or application/json")
	}
}
