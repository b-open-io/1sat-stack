package paymail

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/bsv-blockchain/arcade/models"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk/primitives"
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
	host := c.Hostname()
	scheme := c.Protocol()
	base := fmt.Sprintf("%s://%s%s", scheme, host, r.pathPrefix)

	return c.JSON(fiber.Map{
		"bsvalias": "1.0",
		"capabilities": fiber.Map{
			"6745385c3fc0": false,
			"pki":          base + "/id/{alias}@{domain.tld}",
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
// @Router /v1/bsvalias/id/{paymail} [get]
func (r *Routes) PKI(c *fiber.Ctx) error {
	paymailAddr := c.Params("paymail")
	alias, _, err := parsePaymail(paymailAddr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Resolve identity key for the alias
	identityKey, err := r.service.ResolveIdentityKey(c.Context(), alias)
	if err != nil {
		r.logger.Warn("PKI lookup failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paymail not found"})
	}

	return c.JSON(fiber.Map{
		"bsvalias": "1.0",
		"handle":   paymailAddr,
		"pubkey":   identityKey.ToDERHex(),
	})
}

// paymentDestinationRequest is the JSON body for the destinations endpoint.
type paymentDestinationRequest struct {
	Satoshis uint64 `json:"satoshis"`
}

// PaymentDestination generates a BRC-29 payment destination for a paymail address.
// @Summary Get P2P payment destination
// @Description Generates a BRC-29 payment destination with output script and reference
// @Tags paymail
// @Accept json
// @Produce json
// @Param paymail path string true "Paymail address (alias@domain)"
// @Param body body paymentDestinationRequest true "Payment request"
// @Success 200 {object} object{reference=string,outputs=[]object{satoshis=int,script=string}}
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Router /v1/bsvalias/p2p-payment-destination/{paymail} [post]
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

	identityKey, err := r.service.ResolveIdentityKey(c.Context(), alias)
	if err != nil {
		r.logger.Warn("payment destination lookup failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paymail not found"})
	}

	pending, err := r.service.DerivePaymentDestination(c.Context(), alias, domain, identityKey, req.Satoshis)
	if err != nil {
		r.logger.Error("failed to derive payment destination", "alias", alias, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "derivation failed"})
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
// @Router /v1/bsvalias/receive-beef/{paymail} [post]
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

	// Broadcast through Arcade synchronously — sender gets immediate feedback
	status, err := r.service.Arcade().SubmitTransaction(c.Context(), beefBytes, nil)
	if err != nil {
		r.logger.Error("arcade broadcast failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("broadcast failed: %v", err)})
	}
	if status != nil && status.Status == models.StatusRejected {
		r.logger.Warn("arcade rejected transaction", "alias", alias, "extraInfo", status.ExtraInfo)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "transaction rejected by network"})
	}

	// Record the txid before internalization so we can trace back if it fails
	pending.TxID = txid.String()
	if err := r.service.Store().Update(c.Context(), pending); err != nil {
		r.logger.Error("failed to update pending payment with txid", "alias", alias, "error", err)
	}

	// Internalize the payment into the wallet
	err = r.internalizePayment(c, alias, beefBytes, uint32(outputIndex), pending)
	if err != nil {
		r.logger.Error("failed to internalize payment", "alias", alias, "txid", txid.String(), "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to process payment"})
	}

	return c.JSON(fiber.Map{
		"txid": txid.String(),
		"note": "Payment received and internalized",
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
// @Router /v1/bsvalias/receive-transaction/{paymail} [post]
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

	// Broadcast BEEF through Arcade synchronously
	status, err := r.service.Arcade().SubmitTransaction(c.Context(), beefBytes, nil)
	if err != nil {
		r.logger.Error("arcade broadcast failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("broadcast failed: %v", err)})
	}
	if status != nil && status.Status == models.StatusRejected {
		r.logger.Warn("arcade rejected transaction", "alias", alias, "extraInfo", status.ExtraInfo)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "transaction rejected by network"})
	}

	// Record the txid before internalization
	txid := tx.TxID()
	pending.TxID = txid.String()
	if err := r.service.Store().Update(c.Context(), pending); err != nil {
		r.logger.Error("failed to update pending payment with txid", "alias", alias, "error", err)
	}

	// Internalize the payment into the wallet
	err = r.internalizePayment(c, alias, beefBytes, uint32(outputIndex), pending)
	if err != nil {
		r.logger.Error("failed to internalize raw tx payment", "alias", alias, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to process payment"})
	}

	return c.JSON(fiber.Map{
		"txid": txid.String(),
		"note": "Payment received and internalized",
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

// internalizePayment calls InternalizeAction on the storage provider to record
// the payment for the client identified by the pending payment's identity key.
func (r *Routes) internalizePayment(
	c *fiber.Ctx,
	alias string,
	beefBytes []byte,
	outputIndex uint32,
	pending *PendingPayment,
) error {
	// Resolve the client user from their identity key
	userResp, err := r.service.WalletProvider().FindOrInsertUser(c.Context(), pending.IdentityPubKey)
	if err != nil {
		return fmt.Errorf("failed to resolve wallet user for %s: %w", alias, err)
	}
	authID := wdk.AuthID{
		IdentityKey: pending.IdentityPubKey,
		UserID:      &userResp.User.UserID,
	}

	senderIdentityKey := primitives.PubKeyHex(r.service.AnyoneDeriverIdentityKey().ToDERHex())

	args := wdk.InternalizeActionArgs{
		Tx:          beefBytes,
		Description: primitives.String5to2000Bytes(fmt.Sprintf("Paymail payment to %s", alias)),
		Labels:      []primitives.StringUnder300{primitives.NewIdentifier("paymail"), primitives.NewIdentifier("incoming")},
	}

	if pending.Satoshis == 1 {
		_, tx, _, err := transaction.ParseBeef(beefBytes)
		if err != nil {
			return fmt.Errorf("failed to parse BEEF: %w", err)
		}
		if int(outputIndex) >= len(tx.Outputs) {
			return fmt.Errorf("output index %d out of range", outputIndex)
		}

		contentType, content, _, _ := ordfs.ParseOutputForContent(tx.Outputs[outputIndex])

		basket := "1sat"
		switch contentType {
		case "application/op-ns":
			basket = "opns"
		case "application/bsv-20":
			r.logger.Warn("received BSV-21 token via paymail, treating as generic ordinal",
				"alias", alias,
				"outputIndex", outputIndex,
			)
		}

		var tags []primitives.StringUnder300
		if contentType != "" {
			tags = append(tags, primitives.NewIdentifier("type:"+contentType))
		}
		if contentType == "application/op-ns" && content != nil {
			if name := strings.TrimSpace(string(content)); name != "" {
				tags = append(tags, primitives.NewIdentifier("name:"+name))
			}
		}

		customInstructions := fmt.Sprintf(`{"protocolID":[2,"3241645161d8"],"keyID":"%s %s"}`,
			pending.DerivationPrefix, pending.DerivationSuffix)
		args.Outputs = []*wdk.InternalizeOutput{
			{
				OutputIndex: outputIndex,
				Protocol:    wdk.BasketInsertionProtocol,
				InsertionRemittance: &wdk.BasketInsertion{
					Basket:             primitives.NewIdentifier(basket),
					CustomInstructions: &customInstructions,
					Tags:               tags,
				},
			},
		}

		r.logger.Info("ordinal payment internalized",
			"alias", alias,
			"basket", basket,
			"contentType", contentType,
			"outputIndex", outputIndex,
		)
	} else {
		args.Outputs = []*wdk.InternalizeOutput{
			{
				OutputIndex: outputIndex,
				Protocol:    wdk.WalletPaymentProtocol,
				PaymentRemittance: &wdk.WalletPayment{
					DerivationPrefix:  primitives.Base64String(pending.DerivationPrefix),
					DerivationSuffix:  primitives.Base64String(pending.DerivationSuffix),
					SenderIdentityKey: senderIdentityKey,
				},
			},
		}
	}

	result, err := r.service.WalletProvider().InternalizeAction(c.Context(), authID, args)
	if err != nil {
		return fmt.Errorf("InternalizeAction failed: %w", err)
	}
	if !result.Accepted {
		return fmt.Errorf("wallet rejected the payment")
	}

	if pending.Satoshis != 1 {
		r.logger.Info("payment internalized",
			"alias", alias,
			"satoshis", pending.Satoshis,
			"outputIndex", outputIndex,
		)
	}
	return nil
}
