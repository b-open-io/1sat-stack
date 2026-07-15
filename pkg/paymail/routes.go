package paymail

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/b-open-io/1sat-stack/pkg/httputil"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for paymail endpoints.
type Routes struct {
	service    *Service
	logger     *slog.Logger
	pathPrefix string
}

// NewRoutes creates a new Routes instance.
func NewRoutes(service *Service, logger *slog.Logger, pathPrefix string) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		service:    service,
		logger:     logger,
		pathPrefix: pathPrefix,
	}
}

// SetPathPrefix sets the full URL path prefix for capability URL generation.
func (r *Routes) SetPathPrefix(prefix string) {
	r.pathPrefix = prefix
}

// Register registers paymail routes with the Fiber router.
func (r *Routes) Register(router fiber.Router) {
	router.Get("/id/:paymail", r.PKI)
	router.Get("/public-profile/:paymail", r.PublicProfile)
	router.Post("/p2p-payment-destination/:paymail", r.PaymentDestination)
	router.Post("/receive-beef/:paymail", r.ReceiveBeef)
	router.Post("/receive-transaction/:paymail", r.ReceiveTransaction)
}

// RegisterWellKnown registers the /.well-known/bsvalias capability discovery endpoint.
func (r *Routes) RegisterWellKnown(app *fiber.App) {
	app.Get("/.well-known/bsvalias", r.Capabilities)
}

// parsePaymail splits "alias@domain" and returns the alias portion.
func parsePaymail(paymailAddr string) (alias string, domain string, err error) {
	parts := strings.SplitN(paymailAddr, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid paymail address: %s", paymailAddr)
	}
	return parts[0], parts[1], nil
}

// Capabilities returns the BSV alias capability discovery document.
// @Summary BSV alias capability discovery
// @Description Returns the .well-known/bsvalias capability document listing supported paymail features
// @Tags paymail
// @Produce json
// @Success 200 {object} object{bsvalias=string,capabilities=object}
// @Router /.well-known/bsvalias [get]
func (r *Routes) Capabilities(c *fiber.Ctx) error {
	httputil.SetShortCache(c, 300)
	host := c.Hostname()
	scheme := c.Protocol()
	base := fmt.Sprintf("%s://%s%s", scheme, host, r.pathPrefix)

	return c.JSON(fiber.Map{
		"bsvalias": "1.0",
		"capabilities": fiber.Map{
			"6745385c3fc0": false,
			"pki":          base + "/id/{alias}@{domain.tld}",
			"0c4339ef99c2": base + "/id/{alias}@{domain.tld}",
			"f12f968c92d6": base + "/public-profile/{alias}@{domain.tld}",
			"2a40af698840": base + "/p2p-payment-destination/{alias}@{domain.tld}",
			"5c55a7fdb7bb": base + "/receive-beef/{alias}@{domain.tld}",
			"5f1323cddf31": base + "/receive-transaction/{alias}@{domain.tld}",
		},
	})
}

// PKI returns the public key infrastructure response for a paymail address.
// @Summary Get public key for paymail address
// @Description Returns the identity public key for a paymail address
// @Tags paymail
// @Produce json
// @Param paymail path string true "Paymail address (alias@domain)"
// @Success 200 {object} object{bsvalias=string,handle=string,pubkey=string}
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Router /id/{paymail} [get]
func (r *Routes) PKI(c *fiber.Ctx) error {
	paymailAddr := c.Params("paymail")
	alias, _, err := parsePaymail(paymailAddr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	resolved, err := r.service.ResolveName(c.Context(), alias)
	if err != nil {
		r.logger.Warn("PKI lookup failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paymail not found"})
	}
	if resolved.IdentityKey == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no identity key registered"})
	}

	return c.JSON(fiber.Map{
		"bsvalias": "1.0",
		"handle":   paymailAddr,
		"pubkey":   resolved.IdentityKey.ToDERHex(),
	})
}

// PublicProfile returns the public profile for a paymail address.
// @Summary Get public profile for paymail address
// @Description Returns the display name for a paymail address
// @Tags paymail
// @Produce json
// @Param paymail path string true "Paymail address (alias@domain)"
// @Success 200 {object} object{name=string}
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Router /public-profile/{paymail} [get]
func (r *Routes) PublicProfile(c *fiber.Ctx) error {
	alias, _, err := parsePaymail(c.Params("paymail"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	if _, err := r.service.ResolveName(c.Context(), alias); err != nil {
		r.logger.Warn("public profile lookup failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paymail not found"})
	}

	return c.JSON(fiber.Map{"name": alias})
}

// paymentDestinationRequest is the JSON body for the destinations endpoint.
type paymentDestinationRequest struct {
	Satoshis uint64 `json:"satoshis"`
}

// PaymentDestination generates a payment destination for a paymail address.
// @Summary Get P2P payment destination
// @Description Generates a payment destination from opns.idKey: BRC-29 when a hex identity key is registered, or a fixed P2PKH script for a legacy Base58 address
// @Tags paymail
// @Accept json
// @Produce json
// @Param paymail path string true "Paymail address (alias@domain)"
// @Param body body paymentDestinationRequest true "Payment request"
// @Success 200 {object} object{reference=string,outputs=[]object{satoshis=int,script=string}}
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Router /p2p-payment-destination/{paymail} [post]
func (r *Routes) PaymentDestination(c *fiber.Ctx) error {
	paymailAddr := c.Params("paymail")
	alias, domain, err := parsePaymail(paymailAddr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	var req paymentDestinationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	resolved, err := r.service.ResolveName(c.Context(), alias)
	if err != nil {
		r.logger.Warn("payment destination lookup failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paymail not found"})
	}

	if !resolved.CanReceive() {
		r.logger.Warn("payment destination undeliverable", "alias", alias, "error", ErrUndeliverable)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paymail cannot receive payments: no payment binding registered"})
	}

	var pending *PendingPayment
	if resolved.IdentityKey != nil {
		pending, err = r.service.DerivePaymentDestination(c.Context(), alias, domain, resolved.IdentityKey, req.Satoshis)
		if err != nil {
			r.logger.Error("failed to derive payment destination", "alias", alias, "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "derivation failed"})
		}
	} else {
		pending, err = r.service.AddressPaymentDestination(c.Context(), alias, domain, resolved.Address, req.Satoshis)
		if err != nil {
			r.logger.Error("failed to build address payment destination", "alias", alias, "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "destination failed"})
		}
	}

	return c.JSON(fiber.Map{
		"reference": pending.Reference,
		"outputs": []fiber.Map{
			{
				"satoshis": pending.Satoshis,
				"script":   pending.OutputScript,
			},
		},
	})
}

// p2pMetadata is the optional metadata object sent with P2P transactions.
type p2pMetadata struct {
	Sender string `json:"sender,omitempty"`
	Note   string `json:"note,omitempty"`
}

// rejectBroadcast reports whether a Submit result should abort the payment, and
// if so writes the response. A wait timeout is tolerated when arcade still
// confirms the network accepted the transaction: arcade only emits a status
// event on a state transition, so a txid the sender already broadcast produces
// no event and the wait burns its full deadline on a healthy transaction.
func (r *Routes) rejectBroadcast(
	c *fiber.Ctx,
	alias string,
	status *arcadeclient.TransactionStatus,
	err error,
) (bool, error) {
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		r.logger.Error("broadcast failed", "alias", alias, "error", err)
		return true, c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("broadcast failed: %v", err)})
	}
	if status != nil && arcadeclient.IsRejected(status.TxStatus) {
		r.logger.Warn("arcade rejected transaction", "alias", alias, "tx_status", status.TxStatus, "extraInfo", status.ExtraInfo)
		return true, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "transaction rejected by network"})
	}
	if err != nil && (status == nil || !arcadeclient.IsAccepted(status.TxStatus)) {
		txStatus := ""
		if status != nil {
			txStatus = status.TxStatus
		}
		r.logger.Error("broadcast unconfirmed after wait timeout", "alias", alias, "tx_status", txStatus)
		return true, c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "broadcast unconfirmed"})
	}
	return false, nil
}

// receiveBeefRequest is the JSON body for the receive-beef endpoint.
type receiveBeefRequest struct {
	Beef      string       `json:"beef"`
	Reference string       `json:"reference"`
	Metadata  *p2pMetadata `json:"metadata,omitempty"`
}

// ReceiveBeef processes an incoming BEEF transaction payment.
// @Summary Receive BEEF payment
// @Description Receives a BEEF-encoded transaction payment, verifies it, broadcasts via Arcade, and internalizes into wallet
// @Tags paymail
// @Accept json
// @Produce json
// @Param paymail path string true "Paymail address (alias@domain)"
// @Param body body receiveBeefRequest true "BEEF payment"
// @Success 200 {object} object{txid=string,note=string}
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Failure 502 {object} object{error=string}
// @Router /receive-beef/{paymail} [post]
func (r *Routes) ReceiveBeef(c *fiber.Ctx) error {
	paymailAddr := c.Params("paymail")
	alias, _, err := parsePaymail(paymailAddr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	var req receiveBeefRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if req.Reference == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing reference"})
	}

	// Look up pending payment
	pending, err := r.service.Store().Get(c.Context(), req.Reference)
	if err != nil {
		r.logger.Error("store lookup failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if pending == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "destination not found or expired"})
	}

	// Parse BEEF
	beefBytes, err := hex.DecodeString(req.Beef)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid beef hex"})
	}

	_, tx, txid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("invalid BEEF: %v", err)})
	}

	// Verify payment: find the output matching our expected script and amount
	outputIndex, err := verifyPayment(tx, pending)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Broadcast through arcade (via internal handler) — sender gets immediate feedback
	status, err := r.service.Handler().Submit(c.Context(), beefBytes)
	if rejected, resp := r.rejectBroadcast(c, alias, status, err); rejected {
		return resp
	}

	// Record the txid before internalization so we can trace back if it fails
	pending.TxID = txid[:]
	if err := r.service.Store().Update(c.Context(), pending); err != nil {
		r.logger.Error("failed to update pending payment with txid", "alias", alias, "error", err)
	}

	// Deliver payment to message box for client-side internalization
	if err := r.service.DeliverToMessageBox(beefBytes, uint32(outputIndex), pending); err != nil {
		r.logger.Error("failed to deliver payment to message box", "alias", alias, "txid", txid.String(), "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to process payment"})
	}

	return c.JSON(fiber.Map{
		"txid": txid.String(),
		"note": "Payment received",
	})
}

// receiveTransactionRequest is the JSON body for the receive-transaction endpoint.
type receiveTransactionRequest struct {
	Hex       string       `json:"hex"`
	Reference string       `json:"reference"`
	Metadata  *p2pMetadata `json:"metadata,omitempty"`
}

// ReceiveTransaction processes an incoming raw transaction payment.
// @Summary Receive raw transaction payment
// @Description Receives a raw hex transaction payment, verifies it, broadcasts via Arcade, and internalizes into wallet
// @Tags paymail
// @Accept json
// @Produce json
// @Param paymail path string true "Paymail address (alias@domain)"
// @Param body body receiveTransactionRequest true "Transaction payment"
// @Success 200 {object} object{txid=string,note=string}
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Failure 502 {object} object{error=string}
// @Router /receive-transaction/{paymail} [post]
func (r *Routes) ReceiveTransaction(c *fiber.Ctx) error {
	paymailAddr := c.Params("paymail")
	alias, _, err := parsePaymail(paymailAddr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	var req receiveTransactionRequest
	if err := c.BodyParser(&req); err != nil {
		r.logger.Warn("receive-transaction: body parse failed", "alias", alias, "error", err, "body", string(c.Body()))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if req.Reference == "" {
		r.logger.Warn("receive-transaction: missing reference", "alias", alias, "hex_len", len(req.Hex))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing reference"})
	}

	// Look up pending payment
	pending, err := r.service.Store().Get(c.Context(), req.Reference)
	if err != nil {
		r.logger.Error("store lookup failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if pending == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "destination not found or expired"})
	}

	// Parse raw transaction
	tx, err := transaction.NewTransactionFromHex(req.Hex)
	if err != nil {
		r.logger.Warn("receive-transaction: invalid tx hex", "alias", alias, "error", err, "hex_len", len(req.Hex))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid transaction hex"})
	}

	// Verify payment
	outputIndex, err := verifyPayment(tx, pending)
	if err != nil {
		r.logger.Warn("receive-transaction: payment verification failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Build atomic BEEF from raw transaction by populating ancestor chain
	if err := r.service.BeefStorage().PopulateAncestors(c.Context(), tx); err != nil {
		r.logger.Error("failed to populate ancestors for raw tx", "alias", alias, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to build BEEF from transaction"})
	}
	beefBytes, err := tx.AtomicBEEF(false)
	if err != nil {
		r.logger.Error("failed to serialize atomic BEEF", "alias", alias, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to build BEEF from transaction"})
	}

	// Broadcast BEEF through arcade (via internal handler)
	status, err := r.service.Handler().Submit(c.Context(), beefBytes)
	if rejected, resp := r.rejectBroadcast(c, alias, status, err); rejected {
		return resp
	}

	// Record the txid before internalization
	txid := tx.TxID()
	pending.TxID = txid[:]
	if err := r.service.Store().Update(c.Context(), pending); err != nil {
		r.logger.Error("failed to update pending payment with txid", "alias", alias, "error", err)
	}

	// Deliver payment to message box for client-side internalization
	if err := r.service.DeliverToMessageBox(beefBytes, uint32(outputIndex), pending); err != nil {
		r.logger.Error("failed to deliver payment to message box", "alias", alias, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to process payment"})
	}

	return c.JSON(fiber.Map{
		"txid": txid.String(),
		"note": "Payment received",
	})
}

// verifyPayment checks that the transaction contains an output matching the pending payment.
func verifyPayment(tx *transaction.Transaction, pending *PendingPayment) (int, error) {
	for i, output := range tx.Outputs {
		scriptHex := hex.EncodeToString(*output.LockingScript)
		if scriptHex == pending.OutputScript {
			if output.Satoshis == pending.Satoshis || pending.Satoshis == 0 {
				return i, nil
			}
		}
	}
	return -1, fmt.Errorf("no output matches the expected payment destination")
}
