package faucet

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/auth"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// Handlers wraps the faucet service with Fiber handler methods.
type Handlers struct {
	Svc *Service
}

// ---------------------------------------------------------------------------
// Identity helper
// ---------------------------------------------------------------------------

func identityFromCtx(c *fiber.Ctx) *ec.PublicKey {
	return auth.GetIdentity(c)
}

func identityHexFromCtx(c *fiber.Ctx) string {
	identity := identityFromCtx(c)
	if identity == nil {
		return ""
	}
	return identity.ToDERHex()
}

// ---------------------------------------------------------------------------
// Slug helpers
// ---------------------------------------------------------------------------

var (
	nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)
	leadingTrailing = regexp.MustCompile(`^-+|-+$`)
)

func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	s = leadingTrailing.ReplaceAllString(s, "")
	if s == "" {
		s = "droplit"
	}
	return s
}

func (store *Store) GenerateUniqueSlug(ctx context.Context, name string) (string, error) {
	base := Slugify(name)
	candidate := base

	for i := 2; ; i++ {
		_, err := store.LoadFaucetConfigBySlug(ctx, candidate)
		if err != nil {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

// ---------------------------------------------------------------------------
// Authorization helpers
// ---------------------------------------------------------------------------

func (s *Service) IsAuthorizedForFaucet(ctx context.Context, faucetSlug string, userPubKey string) (string, bool, error) {
	cfg, err := s.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return "", false, err
	}

	if cfg.OwnerUserPublicKeyHex.Valid && cfg.OwnerUserPublicKeyHex.String == userPubKey {
		return cfg.FaucetName, true, nil
	}

	hashedPubKey := HashPubKey(userPubKey)
	_, lookupErr := s.Store.LookupAPIKeyByHash(ctx, cfg.FaucetName, hashedPubKey)
	if lookupErr == nil {
		return cfg.FaucetName, true, nil
	}

	return cfg.FaucetName, false, nil
}

func (s *Service) RequireFaucetAuthorization(ctx context.Context, faucetSlug string, userPubKey string) (string, int, error) {
	faucetName, authorized, err := s.IsAuthorizedForFaucet(ctx, faucetSlug, userPubKey)
	if err != nil {
		return "", fiber.StatusInternalServerError, err
	}
	if !authorized {
		return "", fiber.StatusForbidden, fmt.Errorf("you are not authorized to operate faucet %q", faucetSlug)
	}
	return faucetName, 0, nil
}

func (s *Service) VerifyFaucetOwnership(ctx context.Context, faucetSlug string, userPubKey string) (string, int, error) {
	cfg, err := s.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return "", fiber.StatusNotFound, fmt.Errorf("faucet %q not found", faucetSlug)
	}
	if !cfg.OwnerUserPublicKeyHex.Valid || cfg.OwnerUserPublicKeyHex.String != userPubKey {
		return "", fiber.StatusForbidden, fmt.Errorf("you are not authorized to manage faucet %q", faucetSlug)
	}
	return cfg.FaucetName, 0, nil
}

// ---------------------------------------------------------------------------
// SSE listener infrastructure
// ---------------------------------------------------------------------------

var sseListeners = struct {
	mu     sync.Mutex
	faucet map[string]map[string]chan *FaucetActivityItem
}{
	faucet: make(map[string]map[string]chan *FaucetActivityItem),
}

func addSSEListener(faucetName string, listenerID string) chan *FaucetActivityItem {
	sseListeners.mu.Lock()
	defer sseListeners.mu.Unlock()
	if _, ok := sseListeners.faucet[faucetName]; !ok {
		sseListeners.faucet[faucetName] = make(map[string]chan *FaucetActivityItem)
	}
	ch := make(chan *FaucetActivityItem, 10)
	sseListeners.faucet[faucetName][listenerID] = ch
	return ch
}

func removeSSEListener(faucetName string, listenerID string) {
	sseListeners.mu.Lock()
	defer sseListeners.mu.Unlock()
	if listenersForFaucet, ok := sseListeners.faucet[faucetName]; ok {
		if ch, ok := listenersForFaucet[listenerID]; ok {
			close(ch)
			delete(listenersForFaucet, listenerID)
		}
		if len(listenersForFaucet) == 0 {
			delete(sseListeners.faucet, faucetName)
		}
	}
}

// DistributeActivityEvent logs an activity to the DB and fans out to SSE listeners.
func (s *Service) DistributeActivityEvent(ctx context.Context, activity *FaucetActivityItem) {
	if activity == nil {
		return
	}

	var detailsStr string
	if activity.Details != nil {
		detailsStr = string(activity.Details)
	}

	if err := s.Store.LogActivity(ctx,
		activity.FaucetName,
		string(activity.Type),
		activity.TxID,
		activity.AmountSat,
		activity.Description,
		detailsStr,
	); err != nil {
		s.Logger.Error("failed to log activity", "faucet", activity.FaucetName, "type", activity.Type, "error", err)
	}

	sseListeners.mu.Lock()
	listenersForFaucet, ok := sseListeners.faucet[activity.FaucetName]
	if ok {
		currentListeners := make(map[string]chan *FaucetActivityItem, len(listenersForFaucet))
		for id, ch := range listenersForFaucet {
			currentListeners[id] = ch
		}
		sseListeners.mu.Unlock()

		for _, ch := range currentListeners {
			select {
			case ch <- activity:
			default:
				slog.Warn("SSE listener channel full, message dropped", "faucet", activity.FaucetName)
			}
		}
	} else {
		sseListeners.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// Admin guard middleware
// ---------------------------------------------------------------------------

// AdminGuard returns a Fiber middleware that restricts access to admin users.
// TODO: implement proper admin check against config or admin pubkey list.
func (h *Handlers) AdminGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if identityHexFromCtx(c) == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
		return c.Next()
	}
}

// ---------------------------------------------------------------------------
// Public handlers
// ---------------------------------------------------------------------------

func (h *Handlers) GetFaucetStatus(c *fiber.Ctx) error {
	faucetSlug := c.Params("slug")
	ctx := c.UserContext()

	config, err := h.Svc.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": fmt.Sprintf("faucet %q not found", faucetSlug)})
	}

	resp := &FaucetStatusResponse{
		FaucetName: config.FaucetName,
		Slug:       config.Slug,
	}
	if config.FixedDropSats.Valid {
		resp.FixedDropSatoshis = config.FixedDropSats.Int64
	}

	if balance, err := h.Svc.GetFaucetBalance(ctx, config.FaucetName); err == nil {
		resp.BalanceSatoshis = balance.BalanceSatoshis
		resp.SpendableUTXOCount = balance.SpendableUTXOs
		resp.UnspentUTXOCount = balance.SpendableUTXOs
	}

	return c.Status(fiber.StatusOK).JSON(resp)
}

// ---------------------------------------------------------------------------
// Authenticated handlers
// ---------------------------------------------------------------------------

func (h *Handlers) CreateFaucet(c *fiber.Ctx) error {
	ctx := c.UserContext()
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	var params CreateUserFaucetRequest
	if err := c.BodyParser(&params); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}
	if params.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "faucet name cannot be empty"})
	}

	actualDropSats := int64(0)
	if params.FixedDropSats != 0 {
		actualDropSats = params.FixedDropSats
	}
	actualMaxConsolidation := h.Svc.Config.DefaultMaxConsolidationInputs
	if params.MaxConsolidationInputs > 0 {
		actualMaxConsolidation = params.MaxConsolidationInputs
	}

	apiKeyBytes := make([]byte, 32)
	if _, err := rand.Read(apiKeyBytes); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate API key"})
	}
	faucetAPIKey := hex.EncodeToString(apiKeyBytes)
	faucetAPIKeyHash := HashAPIKey(faucetAPIKey)

	slug, err := h.Svc.Store.GenerateUniqueSlug(ctx, params.Name)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate slug"})
	}

	counterpartyPubKey, err := ec.PublicKeyFromString(identityHex)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("invalid identity key: %v", err)})
	}

	childPrivKey, err := h.Svc.ServerKey().DeriveChild(counterpartyPubKey, params.Name)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to derive instance master key: %v", err)})
	}
	instancePubKeyHex := hex.EncodeToString(childPrivKey.PubKey().Compressed())

	cfg := &FaucetConfig{
		FaucetName:                 params.Name,
		Slug:                       slug,
		InstanceMasterPublicKeyHex: instancePubKeyHex,
		DerivationCpPubKeyHex:      identityHex,
		DerivationInvoiceNum:       params.Name,
		FixedDropSats:              sql.NullInt64{Int64: actualDropSats, Valid: actualDropSats != 0},
		APIKeyHash:                 sql.NullString{String: faucetAPIKeyHash, Valid: true},
		MaxConsolidationInputs:     sql.NullInt64{Int64: actualMaxConsolidation, Valid: true},
		OwnerUserPublicKeyHex:      sql.NullString{String: identityHex, Valid: true},
	}

	rows, err := h.Svc.Store.SaveFaucetConfig(ctx, cfg)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to insert faucet config"})
	}
	if rows == 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("faucet %q already exists", params.Name)})
	}

	h.registerGlobalKey(ctx, identityHex)

	return c.Status(fiber.StatusCreated).JSON(CreateUserFaucetResponse{
		FaucetName:      params.Name,
		Slug:            slug,
		InstancePubKey:  instancePubKeyHex,
		OwnerPubKey:     identityHex,
		GeneratedAPIKey: faucetAPIKey,
		Message:         fmt.Sprintf("Faucet %q created successfully.", params.Name),
	})
}

func (h *Handlers) FaucetActions(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.RequireFaucetAuthorization(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var req FaucetActionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}
	if req.Kind == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "kind is required"})
	}

	res, err := h.Svc.ExecuteWalletAction(ctx, faucetName, &req, OriginatorFromIdentity(identityHex))
	if err != nil {
		h.Svc.Logger.Error("FaucetActions: action failed", "faucet", faucetName, "kind", req.Kind, "error", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusOK).JSON(res)
}

func (h *Handlers) TapFaucet(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.RequireFaucetAuthorization(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var payload TapRequestPayload
	if err := c.BodyParser(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	resp := &TapResponse{}

	recipientAddress := payload.RecipientAddress
	if recipientAddress == "" {
		faucetCfg, derr := h.Svc.Store.LoadFaucetConfig(ctx, faucetName)
		if derr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to load faucet config: %v", derr)})
		}
		instancePriv, derr := h.Svc.DeriveInstanceMasterKey(faucetCfg)
		if derr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to derive faucet key: %v", derr)})
		}

		requesterPub, derr := ec.PublicKeyFromString(identityHex)
		if derr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("invalid requester pubkey: %v", derr)})
		}

		prefixBytes := make([]byte, 10)
		suffixBytes := make([]byte, 10)
		if _, err := rand.Read(prefixBytes); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate derivation prefix"})
		}
		if _, err := rand.Read(suffixBytes); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate derivation suffix"})
		}
		derivationPrefix := base64.StdEncoding.EncodeToString(prefixBytes)
		derivationSuffix := base64.StdEncoding.EncodeToString(suffixBytes)
		keyID := derivationPrefix + " " + derivationSuffix

		derivedPriv, derr := instancePriv.DeriveChild(requesterPub, keyID)
		if derr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to derive payment key: %v", derr)})
		}
		derivedAddr, derr := script.NewAddressFromPublicKey(derivedPriv.PubKey(), true)
		if derr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to derive address: %v", derr)})
		}

		recipientAddress = derivedAddr.AddressString
		senderPubHex := hex.EncodeToString(instancePriv.PubKey().Compressed())

		h.Svc.Logger.Info("tap derivation",
			"faucet", faucetName,
			"requester", identityHex,
			"derivationPrefix", derivationPrefix,
			"derivationSuffix", derivationSuffix,
			"derivedAddress", recipientAddress,
			"senderIdentityKey", senderPubHex,
			"satoshis", payload.Satoshis,
		)

		resp.DerivationPrefix = derivationPrefix
		resp.DerivationSuffix = derivationSuffix
		resp.SenderIdentityKey = senderPubHex
	}

	// Log derivation details BEFORE createAction so we have recovery info if it crashes
	var amountForLog int64
	if payload.Satoshis != nil {
		amountForLog = int64(*payload.Satoshis)
	}
	details, _ := json.Marshal(map[string]string{
		"recipientAddress":  recipientAddress,
		"requester":         identityHex,
		"derivationPrefix":  resp.DerivationPrefix,
		"derivationSuffix":  resp.DerivationSuffix,
		"senderIdentityKey": resp.SenderIdentityKey,
	})
	h.Svc.DistributeActivityEvent(ctx, &FaucetActivityItem{
		Timestamp:   time.Now(),
		Type:        ActivityTypeTap,
		FaucetName:  faucetName,
		AmountSat:   amountForLog,
		Description: fmt.Sprintf("Tap to %s", recipientAddress),
		Details:     details,
	})

	actionReq := &FaucetActionRequest{
		Kind:             FaucetActionTap,
		RecipientAddress: recipientAddress,
		AmountSat:        payload.Satoshis,
		Broadcast:        ptrBool(true),
	}

	res, err := h.Svc.ExecuteWalletAction(ctx, faucetName, actionReq, OriginatorFromIdentity(identityHex))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	h.Svc.DistributeActivityEvent(ctx, &FaucetActivityItem{
		Timestamp:   time.Now(),
		Type:        ActivityTypeTap,
		TxID:        res.TxID,
		FaucetName:  faucetName,
		AmountSat:   amountForLog,
		Description: fmt.Sprintf("Tap broadcast %s", res.TxID),
	})

	resp.TxID = res.TxID
	resp.RawTx = res.RawTx
	return c.Status(fiber.StatusOK).JSON(resp)
}

func (h *Handlers) PushData(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.RequireFaucetAuthorization(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var params PushDataRequest
	if err := c.BodyParser(&params); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}
	if len(params.Data) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "data cannot be empty"})
	}

	shouldBroadcast := true
	if params.Broadcast != nil {
		shouldBroadcast = *params.Broadcast
	}
	shouldFillInputs := shouldBroadcast
	if params.FillInputs != nil {
		shouldFillInputs = *params.FillInputs
	}
	if shouldBroadcast && !shouldFillInputs {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cannot broadcast without filling inputs"})
	}

	if !shouldFillInputs {
		templateTx, err := BuildOpReturnTemplate(params.Data, params.Encoding)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusOK).JSON(PushDataResponse{
			Message: "OP_RETURN template created (no inputs, not broadcast)",
			RawTx:   templateTx.Hex(),
		})
	}

	actionReq := &FaucetActionRequest{
		Kind:      FaucetActionPush,
		Data:      params.Data,
		Encoding:  params.Encoding,
		Broadcast: &shouldBroadcast,
	}

	res, err := h.Svc.ExecuteWalletAction(ctx, faucetName, actionReq, OriginatorFromIdentity(identityHex))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	response := PushDataResponse{TxID: res.TxID, Message: res.Message}
	if res.RawTx != "" {
		response.RawTx = strings.ToLower(res.RawTx)
	}
	return c.Status(fiber.StatusOK).JSON(response)
}

func (h *Handlers) FundTransaction(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.RequireFaucetAuthorization(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var params FundTransactionRequest
	if err := c.BodyParser(&params); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}
	if params.RawTx == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "rawtx cannot be empty"})
	}

	broadcast := true
	if params.Broadcast != nil {
		broadcast = *params.Broadcast
	}

	actionReq := &FaucetActionRequest{
		Kind:      FaucetActionFund,
		RawTx:     params.RawTx,
		Broadcast: &broadcast,
	}

	res, err := h.Svc.ExecuteWalletAction(ctx, faucetName, actionReq, OriginatorFromIdentity(identityHex))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	response := FundTransactionResponse{TxID: res.TxID, Message: res.Message}
	if res.RawTx != "" {
		response.RawTx = strings.ToLower(res.RawTx)
	}
	return c.Status(fiber.StatusOK).JSON(response)
}

func (h *Handlers) MintInscription(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.RequireFaucetAuthorization(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var params MintInscriptionRequest
	if err := c.BodyParser(&params); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	broadcast := true
	if params.Broadcast != nil {
		broadcast = *params.Broadcast
	}

	actionReq := &FaucetActionRequest{
		Kind:             FaucetActionMint,
		RecipientAddress: params.RecipientAddress,
		ContentType:      params.ContentType,
		Inscription:      params.Data,
		Encoding:         params.Encoding,
		BSV21:            params.BSV21,
		Broadcast:        &broadcast,
	}

	res, err := h.Svc.ExecuteWalletAction(ctx, faucetName, actionReq, OriginatorFromIdentity(identityHex))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(MintInscriptionErrorResponse{
			Success: false,
			Error:   "mint_failed",
			Message: err.Error(),
		})
	}

	txSize := 0
	if res.RawTx != "" {
		txSize = len(res.RawTx) / 2
	}

	return c.Status(fiber.StatusOK).JSON(MintInscriptionSuccessResponse{
		Success:         true,
		TxID:            res.TxID,
		InscriptionID:   res.InscriptionID,
		OutputIndex:     0,
		Size:            txSize,
		Broadcasted:     broadcast,
		ContentType:     params.ContentType,
		InscriptionSize: len(params.Data),
	})
}

func (h *Handlers) RequestDepositAddress(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	config, err := h.Svc.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": fmt.Sprintf("faucet %q not found", faucetSlug)})
	}

	instancePriv, err := h.Svc.DeriveInstanceMasterKey(config)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	depositInvoiceNumStr := strconv.FormatInt(config.NextDepositInvoiceIdx, 10)
	depositAddress, _, err := DeriveBRC100DepositAddress(faucetName, depositInvoiceNumStr, instancePriv, h.Svc.network)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to derive deposit address"})
	}

	if err := h.Svc.Store.UpdateFaucetConfig(ctx, config.FaucetName, map[string]any{
		"next_deposit_invoice_idx": config.NextDepositInvoiceIdx + 1,
	}); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update deposit invoice index"})
	}

	resp := &DepositAddressResponse{
		DepositAddress:       depositAddress,
		DepositInvoiceNumber: depositInvoiceNumStr,
		FaucetName:           faucetName,
	}

	details, _ := json.Marshal(map[string]string{
		"deposit_address":        resp.DepositAddress,
		"deposit_invoice_number": resp.DepositInvoiceNumber,
	})
	h.Svc.DistributeActivityEvent(ctx, &FaucetActivityItem{
		FaucetName:  faucetName,
		Timestamp:   time.Now(),
		Type:        "deposit_address_generated",
		Description: fmt.Sprintf("Generated deposit address %s (invoice %s)", resp.DepositAddress, resp.DepositInvoiceNumber),
		Details:     json.RawMessage(details),
	})

	return c.Status(fiber.StatusOK).JSON(resp)
}

func (h *Handlers) GetActivity(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var params GetFaucetActivityParams
	_ = c.BodyParser(&params)

	limit := 50
	if params.Limit > 0 {
		limit = params.Limit
	}
	offset := 0
	if params.Offset > 0 {
		offset = params.Offset
	}

	entries, err := h.Svc.Store.GetActivity(ctx, faucetName, limit, offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error fetching activity log"})
	}

	activities := make([]FaucetActivityItem, 0, len(entries))
	for _, e := range entries {
		item := FaucetActivityItem{
			FaucetName:    faucetName,
			Timestamp:     e.Timestamp,
			Type:          FaucetActivityType(e.Type),
			TxID:          e.TxID,
			AmountSat:     e.AmountSat,
			Description:   e.Description,
			Details:       e.Details,
			PrettyTimeAgo: PrettyTime(e.Timestamp),
		}
		activities = append(activities, item)
	}

	return c.Status(fiber.StatusOK).JSON(GetFaucetActivityResponse{Activities: activities})
}

func (h *Handlers) ActivityStream(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	listenerID := uuid.NewString()

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		fmt.Fprintf(w, "event: connected\ndata: {\"message\": \"Streaming activity for %s...\"}\n\n", faucetName)
		w.Flush()

		ch := addSSEListener(faucetName, listenerID)
		defer removeSSEListener(faucetName, listenerID)

		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fmt.Fprintf(w, "event: ping\ndata: {\"time\": \"%s\"}\n\n", time.Now().Format(time.RFC3339))
				if err := w.Flush(); err != nil {
					return
				}
			case activityItem, chanOk := <-ch:
				if !chanOk {
					return
				}
				jsonData, err := json.Marshal(activityItem)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "event: faucet_activity\ndata: %s\n\n", string(jsonData))
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	})

	return nil
}

func (h *Handlers) GetDiagnostics(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	config, err := h.Svc.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": fmt.Sprintf("faucet %q not found", faucetSlug)})
	}

	return c.Status(fiber.StatusOK).JSON(&FaucetDiagnosticData{
		FaucetName:            faucetName,
		NextDepositInvoiceIdx: config.NextDepositInvoiceIdx,
	})
}

func (h *Handlers) GenerateNewAPIKey(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	if _, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex); err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	apiKeyBytes := make([]byte, 32)
	if _, err := rand.Read(apiKeyBytes); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate API key"})
	}
	apiKey := hex.EncodeToString(apiKeyBytes)
	apiKeyHash := HashAPIKey(apiKey)

	cfg, err := h.Svc.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": fmt.Sprintf("faucet %q not found", faucetSlug)})
	}

	if err := h.Svc.Store.UpdateFaucetConfig(ctx, cfg.FaucetName, map[string]any{
		"api_key_hash": apiKeyHash,
	}); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to store API key hash"})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"raw_api_key": apiKey,
		"message":     "New API key generated. Store this key securely, it will not be shown again.",
	})
}

// ---------------------------------------------------------------------------
// API Key handlers
// ---------------------------------------------------------------------------

func (h *Handlers) ListAPIKeys(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	rows, err := h.Svc.Store.ListAPIKeys(ctx, faucetName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error querying API keys"})
	}

	apiKeys := make([]APIKeyEntry, 0, len(rows))
	for _, k := range rows {
		entry := APIKeyEntry{
			FaucetName:       faucetName,
			UserPublicKeyHex: k.UserPublicKeyHex,
			Label:            k.Label,
			CreatedAt:        k.CreatedAt,
		}
		apiKeys = append(apiKeys, entry)
	}

	return c.Status(fiber.StatusOK).JSON(&ListAPIKeysResponse{Keys: apiKeys})
}

func (h *Handlers) AddAPIKey(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var req AddAPIKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	if err := ValidatePublicKey(req.PublicKey); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("invalid public key: %v", err)})
	}

	hashedPubKey := HashPubKey(req.PublicKey)
	label := ""
	if req.Label != nil {
		label = *req.Label
	}

	rowsAffected, err := h.Svc.Store.AddAPIKey(ctx, faucetName, req.PublicKey, hashedPubKey, label)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error adding API key"})
	}

	details, _ := json.Marshal(map[string]any{"added_pubkey": req.PublicKey, "label": req.Label})
	h.Svc.DistributeActivityEvent(ctx, &FaucetActivityItem{
		FaucetName:  faucetName,
		Timestamp:   time.Now(),
		Type:        "api_key_added",
		Description: fmt.Sprintf("Added API key to faucet %s", faucetName),
		Details:     json.RawMessage(details),
	})

	msg := "API key added successfully"
	if rowsAffected == 0 {
		msg = "API key already exists for this faucet"
	}
	return c.Status(fiber.StatusOK).JSON(&AddAPIKeyResponse{Success: true, Message: msg})
}

func (h *Handlers) DeleteAPIKey(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	publicKey := c.Params("publicKey")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	if publicKey == identityHex {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cannot remove owner's public key from whitelist"})
	}

	rowsAffected, err := h.Svc.Store.DeleteAPIKey(ctx, faucetName, publicKey)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error deleting API key"})
	}
	if rowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "API key not found for this faucet"})
	}

	details, _ := json.Marshal(map[string]string{"removed_pubkey": publicKey})
	h.Svc.DistributeActivityEvent(ctx, &FaucetActivityItem{
		FaucetName:  faucetName,
		Timestamp:   time.Now(),
		Type:        "api_key_removed",
		Description: fmt.Sprintf("Removed API key from faucet %s", faucetName),
		Details:     json.RawMessage(details),
	})

	return c.Status(fiber.StatusOK).JSON(&DeleteAPIKeyResponse{Success: true, Message: "API key removed successfully"})
}

// ---------------------------------------------------------------------------
// Admin handlers
// ---------------------------------------------------------------------------

func (h *Handlers) ListAllFaucets(c *fiber.Ctx) error {
	ctx := c.UserContext()
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	configs, err := h.Svc.Store.ListFaucetConfigsByOwner(ctx, identityHex)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("error querying configs: %v", err)})
	}

	droplits := make([]Droplit, 0, len(configs))
	for _, d := range configs {
		droplit := Droplit{
			Name:                   d.FaucetName,
			Slug:                   d.Slug,
			InstancePubkey:         d.InstanceMasterPublicKeyHex,
			CounterpartyPubkey:     d.DerivationCpPubKeyHex,
			DerivationInvoiceNum:   d.DerivationInvoiceNum,
			NextChangeInvoiceIdx:   d.NextChangeInvoiceIdx,
			NextDepositInvoiceIdx:  d.NextDepositInvoiceIdx,
			DropSats:               NullableInt64{d.FixedDropSats},
			APIKeyHash:             NullableString{d.APIKeyHash},
			TargetBuckets:          NullableInt64{d.TargetBuckets},
			CurrentBucketIdx:       NullableInt64{d.CurrentBucketIdx},
			MaxConsolidationInputs: NullableInt64{d.MaxConsolidationInputs},
			CreatedAt:              d.CreatedAt,
			UpdatedAt:              d.UpdatedAt,
			OwnerPubkey:            NullableString{d.OwnerUserPublicKeyHex},
		}
		if balance, err := h.Svc.GetFaucetBalance(ctx, d.FaucetName); err == nil {
			droplit.BalanceSats = balance.BalanceSatoshis
		}
		droplits = append(droplits, droplit)
	}

	return c.Status(fiber.StatusOK).JSON(&ListFaucetInstancesResponse{Droplits: droplits})
}

// ---------------------------------------------------------------------------
// SSE Deposit Events (public)
// ---------------------------------------------------------------------------

const gorillaPoolAPIBaseURL = "https://ordinals.gorillapool.io/api/"

func (h *Handlers) DepositEvents(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	depositAddress := c.Params("addr")

	if faucetSlug == "" || depositAddress == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "faucet slug and deposit address are required"})
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		fmt.Fprintf(w, "id: %s\nevent: connected\ndata: {\"message\": \"Connected to SSE for %s on address %s\"}\n\n",
			uuid.NewString(), faucetSlug, depositAddress)
		w.Flush()

		type explorerUTXO struct {
			TxHash   string `json:"txid"`
			TxPos    uint32 `json:"vout"`
			Satoshis uint64 `json:"satoshis"`
			Script   string `json:"script"`
		}

		seenUTXOs := make(map[string]bool)
		apiURL := fmt.Sprintf("%stxos/address/%s/unspent?limit=1&bsv20=false&origins=false&refresh=true",
			gorillaPoolAPIBaseURL, depositAddress)

		checkUTXOs := func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
			if err != nil {
				return
			}
			httpResp, err := http.DefaultClient.Do(req)
			if err != nil {
				errData, _ := json.Marshal(map[string]string{"status": "error_fetching_utxos", "detail": err.Error()})
				fmt.Fprintf(w, "id: %s\nevent: error\ndata: %s\n\n", uuid.NewString(), string(errData))
				w.Flush()
				return
			}
			defer httpResp.Body.Close()

			if httpResp.StatusCode != http.StatusOK {
				statusData, _ := json.Marshal(map[string]any{"status": "checking", "utxo_count": 0})
				fmt.Fprintf(w, "id: %s\nevent: checking\ndata: %s\n\n", uuid.NewString(), string(statusData))
				w.Flush()
				return
			}

			var utxos []explorerUTXO
			if err := json.NewDecoder(httpResp.Body).Decode(&utxos); err != nil {
				errData, _ := json.Marshal(map[string]string{"status": "error_parsing_json", "detail": err.Error()})
				fmt.Fprintf(w, "id: %s\nevent: error\ndata: %s\n\n", uuid.NewString(), string(errData))
				w.Flush()
				return
			}

			newDeposit := false
			for _, utxo := range utxos {
				utxoID := fmt.Sprintf("%s:%d", utxo.TxHash, utxo.TxPos)
				if !seenUTXOs[utxoID] {
					seenUTXOs[utxoID] = true
					newDeposit = true
					utxoData, _ := json.Marshal(map[string]any{
						"status":     "deposit_seen",
						"txid":       utxo.TxHash,
						"vout":       utxo.TxPos,
						"satoshis":   utxo.Satoshis,
						"script":     utxo.Script,
						"utxo_count": len(utxos),
					})
					fmt.Fprintf(w, "id: %s\nevent: deposit_seen\ndata: %s\n\n", uuid.NewString(), string(utxoData))
					w.Flush()
				}
			}

			if !newDeposit {
				statusData, _ := json.Marshal(map[string]any{"status": "checking", "utxo_count": len(utxos)})
				fmt.Fprintf(w, "id: %s\nevent: checking\ndata: %s\n\n", uuid.NewString(), string(statusData))
				w.Flush()
			}
		}

		checkUTXOs()

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				checkUTXOs()
			}
		}
	})

	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (h *Handlers) registerGlobalKey(ctx context.Context, pubKeyHex string) {
	hashedKey := HashPubKey(pubKeyHex)
	if _, err := h.Svc.Store.AddAPIKey(ctx, "__global__", pubKeyHex, hashedKey, "global-registration"); err != nil {
		h.Svc.Logger.Error("failed to register global key", "pubkey", pubKeyHex[:16]+"...", "error", err)
	}
}

// ---------------------------------------------------------------------------
// Deposit scanner handlers
// ---------------------------------------------------------------------------

func (h *Handlers) ScanAndIngestDeposits(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	// TODO: implement ScanAndIngestForFaucet once owner sync infrastructure is ported
	return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
		"error":      "deposit scanning not yet implemented",
		"faucet_name": faucetName,
	})
}

func (h *Handlers) SyncFaucetAddresses(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	faucetName, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex)
	if err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var params SyncFaucetAddressesRequest
	if err := c.BodyParser(&params); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	// TODO: implement sync modes once owner sync infrastructure is ported
	return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
		"error":      "address synchronization not yet implemented",
		"faucet_name": faucetName,
	})
}

// ---------------------------------------------------------------------------
// Admin config handlers
// ---------------------------------------------------------------------------

const defaultMaxConsolidationInputsAdmin int64 = 5

func (h *Handlers) ProvisionFaucet(c *fiber.Ctx) error {
	ctx := c.UserContext()
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	var params ProvisionFaucetRequest
	if err := c.BodyParser(&params); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("invalid request body: %v", err)})
	}
	if params.FaucetIdentifierName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "faucet_identifier_name is required"})
	}
	if params.OwnerCounterpartyPubKeyHex == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "owner_counterparty_pubkey_hex is required"})
	}

	actualDropSats := int64(0)
	if params.FixedDropSats != nil {
		actualDropSats = *params.FixedDropSats
	}

	apiKeyBytes := make([]byte, 32)
	if _, err := rand.Read(apiKeyBytes); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate API key"})
	}
	apiKey := hex.EncodeToString(apiKeyBytes)
	apiKeyHash := HashAPIKey(apiKey)

	slug, err := h.Svc.Store.GenerateUniqueSlug(ctx, params.FaucetIdentifierName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate slug"})
	}

	counterpartyPubKey, err := ec.PublicKeyFromString(params.OwnerCounterpartyPubKeyHex)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("invalid counterparty pubkey: %v", err)})
	}

	childPrivKey, err := h.Svc.ServerKey().DeriveChild(counterpartyPubKey, params.FaucetIdentifierName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to derive instance master key: %v", err)})
	}
	instancePubKeyHex := hex.EncodeToString(childPrivKey.PubKey().Compressed())

	actualMaxConsolidation := defaultMaxConsolidationInputsAdmin
	if params.MaxConsolidationInputs != nil {
		actualMaxConsolidation = *params.MaxConsolidationInputs
	}

	cfg := &FaucetConfig{
		FaucetName:                 params.FaucetIdentifierName,
		Slug:                       slug,
		InstanceMasterPublicKeyHex: instancePubKeyHex,
		DerivationCpPubKeyHex:      params.OwnerCounterpartyPubKeyHex,
		DerivationInvoiceNum:       params.FaucetIdentifierName,
		FixedDropSats:              sql.NullInt64{Int64: actualDropSats, Valid: actualDropSats != 0},
		APIKeyHash:                 sql.NullString{String: apiKeyHash, Valid: true},
		TargetBuckets:              sql.NullInt64{Int64: 3, Valid: true},
		MaxConsolidationInputs:     sql.NullInt64{Int64: actualMaxConsolidation, Valid: true},
		OwnerUserPublicKeyHex:      sql.NullString{String: params.OwnerCounterpartyPubKeyHex, Valid: true},
	}

	rows, err := h.Svc.Store.SaveFaucetConfig(ctx, cfg)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create faucet configuration"})
	}
	if rows == 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("faucet %q already exists", params.FaucetIdentifierName)})
	}

	h.registerGlobalKey(ctx, params.OwnerCounterpartyPubKeyHex)

	return c.Status(fiber.StatusCreated).JSON(ProvisionFaucetResponse{
		FaucetIdentifierName:          params.FaucetIdentifierName,
		Slug:                          slug,
		RawAPIKey:                     apiKey,
		Message:                       fmt.Sprintf("Faucet %q created successfully.", params.FaucetIdentifierName),
		FaucetInstanceMasterPubKeyHex: instancePubKeyHex,
		FixedDropSats:                 actualDropSats,
		MaxConsolidationInputs:        actualMaxConsolidation,
	})
}

func (h *Handlers) GetFaucetConfig(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	config, err := h.Svc.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}

	if !config.OwnerUserPublicKeyHex.Valid || config.OwnerUserPublicKeyHex.String != identityHex {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": fmt.Sprintf("you are not authorized to access faucet %q config", faucetSlug)})
	}

	return c.Status(fiber.StatusOK).JSON(config)
}

func (h *Handlers) UpdateFaucetConfig(c *fiber.Ctx) error {
	ctx := c.UserContext()
	faucetSlug := c.Params("slug")
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	if _, status, err := h.Svc.VerifyFaucetOwnership(ctx, faucetSlug, identityHex); err != nil {
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	var params UpdateFaucetConfigAdminRequest
	if err := c.BodyParser(&params); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	updates := make(map[string]any)
	if params.FixedDropSats != nil {
		updates["drop_sats"] = *params.FixedDropSats
	}
	if params.TargetBuckets != nil {
		updates["target_buckets"] = *params.TargetBuckets
	}
	if params.MaxConsolidationInputs != nil {
		updates["max_consolidation_inputs"] = *params.MaxConsolidationInputs
	}
	if params.CurrentBucketIdx != nil {
		updates["current_bucket_idx"] = *params.CurrentBucketIdx
	}

	if len(updates) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no fields to update"})
	}

	cfg, err := h.Svc.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}

	if err := h.Svc.Store.UpdateFaucetConfig(ctx, cfg.FaucetName, updates); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update faucet config"})
	}

	updated, err := h.Svc.Store.LoadFaucetConfigBySlug(ctx, faucetSlug)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusOK).JSON(updated)
}

func (h *Handlers) MigrateOwner(c *fiber.Ctx) error {
	ctx := c.UserContext()
	identityHex := identityHexFromCtx(c)
	if identityHex == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	var req MigrateOwnerRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	if identityHex != req.NewOwnerPubkey {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "new owner pubkey must match authenticated user's pubkey"})
	}

	oldConfigs, err := h.Svc.Store.ListFaucetConfigsByOwner(ctx, req.OldOwnerPubkey)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query faucets"})
	}

	if len(oldConfigs) == 0 {
		return c.Status(fiber.StatusOK).JSON(&MigrateOwnerResponse{
			UpdatedCount: 0,
			FaucetNames:  []string{},
			Message:      "No faucets found with the old owner pubkey",
		})
	}

	faucetNames := make([]string, 0, len(oldConfigs))
	for _, cfg := range oldConfigs {
		faucetNames = append(faucetNames, cfg.FaucetName)
		if err := h.Svc.Store.UpdateFaucetConfig(ctx, cfg.FaucetName, map[string]any{
			"owner_pubkey": req.NewOwnerPubkey,
		}); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to update owner for %q: %v", cfg.FaucetName, err)})
		}
	}

	return c.Status(fiber.StatusOK).JSON(&MigrateOwnerResponse{
		UpdatedCount: len(faucetNames),
		FaucetNames:  faucetNames,
		Message:      fmt.Sprintf("Successfully migrated %d faucet(s) to new owner pubkey", len(faucetNames)),
	})
}
