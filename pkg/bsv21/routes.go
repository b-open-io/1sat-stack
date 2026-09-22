package bsv21

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for BSV21 API
type Routes struct {
	storage *txo.OutputStore
	lookup  *lookuppkg.BSV21Lookup
	manager *TokenManager
	logger  *slog.Logger
}

// RoutesDeps holds dependencies for BSV21 routes
type RoutesDeps struct {
	Storage *txo.OutputStore
	Lookup  *lookuppkg.BSV21Lookup
	Manager *TokenManager
	Logger  *slog.Logger
}

// NewRoutes creates a new Routes instance
func NewRoutes(cfg *RoutesDeps) *Routes {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		storage: cfg.Storage,
		lookup:  cfg.Lookup,
		manager: cfg.Manager,
		logger:  logger,
	}
}

// Register registers the BSV21 routes with the Fiber router
func (r *Routes) Register(router fiber.Router) {
	// Static routes must be registered before parameterized routes.
	// /browse is the token browser; it has to win over /:tokenId.
	registerBrowser(router)
	router.Get("/tokens", r.ListTokens)
	router.Post("/tokens", r.LookupTokens)

	// Output validation routes
	router.Get("/:tokenId/holders", r.ListHolders)
	router.Get("/:tokenId/activity", r.ListActivity)
	router.Post("/:tokenId/outputs", r.ValidateOutputs)
	router.Get("/:tokenId/outputs/:outpoint", r.GetTokenOutput)

	router.Get("/:tokenId", r.GetToken)
	router.Get("/:tokenId/tx/:txid", r.GetTransaction)
	router.Get("/:tokenId/:lockType/:address/balance", r.GetAddressBalance)
	router.Get("/:tokenId/:lockType/:address/history", r.GetAddressHistory)
	router.Get("/:tokenId/:lockType/:address/unspent", r.GetAddressUnspent)
	router.Post("/:tokenId/:lockType/balance", r.GetMultiAddressBalance)
	router.Post("/:tokenId/:lockType/history", r.GetMultiAddressHistory)
	router.Post("/:tokenId/:lockType/unspent", r.GetMultiAddressUnspent)
}

// TokenDetailResponse represents combined BSV21 token details and funding status
// @Description Combined token metadata and funding status
type TokenDetailResponse struct {
	TokenID string       `json:"tokenId"`
	Token   *parse.BSV21 `json:"token"`
	Status  *TokenStatus `json:"status,omitempty"`
}

// OutputData represents an output (or input) in a transaction response
// @Description Output or input data for a transaction
type OutputData struct {
	TxID  *string        `json:"txid,omitempty"` // Source txid (for inputs only)
	Vout  uint32         `json:"vout"`
	Data  map[string]any `json:"data,omitempty"`
	Spend *string        `json:"spend,omitempty"` // Spending txid hex (for outputs only, null if unspent)
}

// TransactionData represents a transaction with its inputs and outputs
// @Description Transaction details with inputs, outputs, and optional BEEF
type TransactionData struct {
	TxID    string        `json:"txid"`
	Inputs  []*OutputData `json:"inputs"`
	Outputs []*OutputData `json:"outputs"`
	Beef    []byte        `json:"beef,omitempty"`
}

// BalanceResponse represents token balance information
type BalanceResponse struct {
	Balance   uint64 `json:"balance"`
	UtxoCount int    `json:"utxoCount"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Message string `json:"message"`
}

var errTokenNotFound = fmt.Errorf("token not found")
var errInvalidTokenID = fmt.Errorf("invalid token ID format")

// ListTokens returns all known tokens with status and metadata
// @Summary List tokens
// @Description Returns active tokens by default. Pass ?all=true to include all known tokens.
// @Tags bsv21
// @Produce json
// @Param all query bool false "Include inactive tokens"
// @Success 200 {array} TokenStatus
// @Router /tokens [get]
func (r *Routes) ListTokens(c *fiber.Ctx) error {
	includeAll := c.Query("all") == "true"
	statuses, err := r.listTokens(c, includeAll)
	if err != nil {
		r.logger.Error("failed to list tokens", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Message: "Failed to list tokens"})
	}
	if statuses == nil {
		statuses = []*TokenStatus{}
	}
	return c.JSON(statuses)
}

// listTokens returns funding status when the sync manager is running, and
// discovery metadata otherwise so the browser still has something to show.
func (r *Routes) listTokens(c *fiber.Ctx, includeAll bool) ([]*TokenStatus, error) {
	if r.manager != nil {
		return r.manager.ListTokenStatuses(c.Context(), includeAll), nil
	}
	if !includeAll || r.lookup == nil {
		return []*TokenStatus{}, nil
	}
	tokens, err := r.lookup.ListTokens(c.Context())
	if err != nil {
		return nil, err
	}
	statuses := make([]*TokenStatus, 0, len(tokens))
	for _, token := range tokens {
		if token == nil {
			continue
		}
		st := NewTokenStatus(token.TokenID, "", 0, 0, 0, false, false)
		st.Symbol = token.Symbol
		st.Decimals = token.Decimals
		st.Icon = token.Icon
		statuses = append(statuses, st)
	}
	return statuses, nil
}

// GetToken retrieves token details and funding status
// @Summary Get token details
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Success 200 {object} TokenDetailResponse
// @Router /{tokenId} [get]
func (r *Routes) GetToken(c *fiber.Ctx) error {
	tokenIdStr := c.Params("tokenId")

	resp, err := r.getTokenDetail(c, tokenIdStr)
	if err != nil {
		if errors.Is(err, errInvalidTokenID) {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Message: err.Error()})
		}
		return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{Message: err.Error()})
	}

	return c.JSON(resp)
}

// ListHolders returns unspent balances grouped by lock and address.
// @Summary List token holders
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param limit query int false "Max holders" default(100)
// @Success 200 {array} lookuppkg.Holder
// @Failure 400 {object} ErrorResponse
// @Router /{tokenId}/holders [get]
func (r *Routes) ListHolders(c *fiber.Ctx) error {
	tokenId, err := canonicalTokenID(c.Params("tokenId"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Message: err.Error()})
	}
	holders, err := r.lookup.ListHolders(c.Context(), tokenId, queryLimit(c, 100, 500))
	if err != nil {
		r.logger.Error("holders lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Message: "Failed to list holders"})
	}
	if holders == nil {
		holders = []*lookuppkg.Holder{}
	}
	return c.JSON(holders)
}

// ListActivity returns indexed token outputs, newest first when rev=true.
// @Summary List token activity
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param limit query int false "Max outputs" default(100)
// @Param rev query bool false "Newest first"
// @Success 200 {array} lookuppkg.TokenRow
// @Failure 400 {object} ErrorResponse
// @Router /{tokenId}/activity [get]
func (r *Routes) ListActivity(c *fiber.Ctx) error {
	tokenId, err := canonicalTokenID(c.Params("tokenId"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Message: err.Error()})
	}
	rows, err := r.lookup.ListActivity(c.Context(), tokenId, queryLimit(c, 100, 500), c.Query("rev") == "true")
	if err != nil {
		r.logger.Error("activity lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Message: "Failed to list activity"})
	}
	if rows == nil {
		rows = []*lookuppkg.TokenRow{}
	}
	return c.JSON(rows)
}

func canonicalTokenID(tokenId string) (string, error) {
	op, err := transaction.OutpointFromString(tokenId)
	if err != nil {
		return "", errInvalidTokenID
	}
	return op.OrdinalString(), nil
}

func queryLimit(c *fiber.Ctx, def, max int) int {
	limit := c.QueryInt("limit", def)
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}

// LookupTokens retrieves details for multiple tokens
// @Summary Lookup tokens (bulk)
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenIds body []string true "Array of token IDs (max 100)"
// @Success 200 {array} TokenDetailResponse
// @Router /tokens [post]
func (r *Routes) LookupTokens(c *fiber.Ctx) error {
	var tokenIds []string
	if err := c.BodyParser(&tokenIds); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(tokenIds) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No token IDs provided",
		})
	}

	if len(tokenIds) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many token IDs (max 100)",
		})
	}

	results := make([]*TokenDetailResponse, 0, len(tokenIds))
	for _, tokenId := range tokenIds {
		resp, err := r.getTokenDetail(c, tokenId)
		if err != nil {
			// Skip tokens that fail to load rather than failing the whole request
			r.logger.Debug("failed to load token in bulk lookup", "tokenId", tokenId, "error", err)
			continue
		}
		results = append(results, resp)
	}

	return c.JSON(results)
}

// getTokenDetail loads combined token data and funding status for a single token ID.
// Returns an error without writing to the response — callers handle their own error responses.
func (r *Routes) getTokenDetail(c *fiber.Ctx, tokenIdStr string) (*TokenDetailResponse, error) {
	outpoint, err := transaction.OutpointFromString(tokenIdStr)
	if err != nil {
		return nil, errInvalidTokenID
	}

	tokenData, err := r.lookup.GetToken(c.Context(), outpoint)
	if err != nil {
		return nil, errTokenNotFound
	}

	resp := &TokenDetailResponse{
		TokenID: tokenIdStr,
		Token:   tokenData,
	}

	if r.manager != nil {
		status, err := r.manager.GetTokenStatus(c.Context(), tokenIdStr)
		if err == nil {
			resp.Status = status
		}
	}

	return resp, nil
}

// GetTransaction retrieves token inputs and outputs for a transaction
// @Summary Get transaction
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param txid path string true "Transaction ID"
// @Param beef query bool false "Include BEEF data"
// @Success 200 {object} TransactionData
// @Router /{tokenId}/tx/{txid} [get]
func (r *Routes) GetTransaction(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	txidStr := c.Params("txid")

	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid transaction ID format",
		})
	}

	includeBeef := c.Query("beef") == "true"

	rawOutputs, err := r.lookup.FindByTxid(c.Context(), tokenId, txid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve transaction details",
		})
	}

	if len(rawOutputs) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
			Message: "Transaction not found",
		})
	}

	var outputs []*OutputData
	for _, out := range rawOutputs {
		od := &OutputData{
			Vout: out.Outpoint.Index,
			Data: out.Data,
		}
		if out.SpendTxid != nil {
			s := out.SpendTxid.String()
			od.Spend = &s
		}
		outputs = append(outputs, od)
	}

	var inputs []*OutputData
	consumedOps, err := r.lookup.GetInputsConsumed(c.Context(), tokenId, &rawOutputs[0].Outpoint)
	if err == nil && len(consumedOps) > 0 {
		inputOutputs, err := r.lookup.LoadOutputs(c.Context(), tokenId, consumedOps)
		if err == nil {
			for _, inp := range inputOutputs {
				if inp == nil {
					continue
				}
				srcTxid := inp.Outpoint.Txid.String()
				inputs = append(inputs, &OutputData{
					TxID: &srcTxid,
					Vout: inp.Outpoint.Index,
					Data: inp.Data,
				})
			}
		}
	}

	tx := &TransactionData{
		TxID:    txidStr,
		Inputs:  inputs,
		Outputs: outputs,
	}

	if includeBeef && r.storage != nil && r.storage.BeefStore != nil {
		beef, err := r.storage.BeefStore.LoadBeef(c.Context(), txid)
		if err == nil && beef != nil {
			tx.Beef, _ = beef.AtomicBytes(txid)
		}
	}

	return c.JSON(tx)
}

// GetAddressBalance retrieves token balance for a single address
// @Summary Get address balance
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param address path string true "Address"
// @Success 200 {object} BalanceResponse
// @Router /{tokenId}/{lockType}/{address}/balance [get]
func (r *Routes) GetAddressBalance(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")
	address := c.Params("address")

	balance, utxoCount, err := r.lookup.GetBalance(c.Context(), tokenId, lockType, address)
	if err != nil {
		r.logger.Error("Balance calculation error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to calculate balance",
		})
	}

	return c.JSON(BalanceResponse{
		Balance:   balance,
		UtxoCount: utxoCount,
	})
}

// GetAddressHistory retrieves transaction history for a single address
// @Summary Get address history
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param address path string true "Address"
// @Success 200 {array} txo.IndexedOutput
// @Router /{tokenId}/{lockType}/{address}/history [get]
func (r *Routes) GetAddressHistory(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")
	address := c.Params("address")

	cfg := parseSearchConfig(c)
	outpoints, err := r.lookup.SearchHistory(c.Context(), tokenId, lockType, address, cfg)
	if err != nil {
		r.logger.Error("History lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve output history",
		})
	}

	outputs, err := r.lookup.LoadOutputs(c.Context(), tokenId, outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// GetAddressUnspent retrieves unspent outputs for a single address
// @Summary Get address unspent
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param address path string true "Address"
// @Success 200 {array} txo.IndexedOutput
// @Router /{tokenId}/{lockType}/{address}/unspent [get]
func (r *Routes) GetAddressUnspent(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")
	address := c.Params("address")

	cfg := parseSearchConfig(c)
	outpoints, err := r.lookup.SearchUTXOs(c.Context(), tokenId, lockType, address, cfg)
	if err != nil {
		r.logger.Error("Unspent lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve unspent outputs",
		})
	}

	outputs, err := r.lookup.LoadOutputs(c.Context(), tokenId, outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// GetMultiAddressBalance retrieves token balance for multiple addresses
// @Summary Get balance (multi-address)
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {object} BalanceResponse
// @Router /{tokenId}/{lockType}/balance [post]
func (r *Routes) GetMultiAddressBalance(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")

	var addresses []string
	if err := c.BodyParser(&addresses); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(addresses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No addresses provided",
		})
	}

	if len(addresses) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many addresses (max 100)",
		})
	}

	balance, utxoCount, err := r.lookup.GetMultiBalance(c.Context(), tokenId, lockType, addresses)
	if err != nil {
		r.logger.Error("Balance calculation error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to calculate balances",
		})
	}

	return c.JSON(BalanceResponse{
		Balance:   balance,
		UtxoCount: utxoCount,
	})
}

// GetMultiAddressHistory retrieves transaction history for multiple addresses
// @Summary Get history (multi-address)
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {array} txo.IndexedOutput
// @Router /{tokenId}/{lockType}/history [post]
func (r *Routes) GetMultiAddressHistory(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")

	var addresses []string
	if err := c.BodyParser(&addresses); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(addresses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No addresses provided",
		})
	}

	if len(addresses) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many addresses (max 100)",
		})
	}

	cfg := parseSearchConfig(c)
	outpoints, err := r.lookup.SearchMultiHistory(c.Context(), tokenId, lockType, addresses, cfg)
	if err != nil {
		r.logger.Error("History lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve output history",
		})
	}

	outputs, err := r.lookup.LoadOutputs(c.Context(), tokenId, outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// GetMultiAddressUnspent retrieves unspent outputs for multiple addresses
// @Summary Get unspent (multi-address)
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {array} txo.IndexedOutput
// @Router /{tokenId}/{lockType}/unspent [post]
func (r *Routes) GetMultiAddressUnspent(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")

	var addresses []string
	if err := c.BodyParser(&addresses); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(addresses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No addresses provided",
		})
	}

	if len(addresses) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many addresses (max 100)",
		})
	}

	cfg := parseSearchConfig(c)
	outpoints, err := r.lookup.SearchMultiUTXOs(c.Context(), tokenId, lockType, addresses, cfg)
	if err != nil {
		r.logger.Error("Unspent lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve unspent outputs",
		})
	}

	outputs, err := r.lookup.LoadOutputs(c.Context(), tokenId, outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// ValidateOutputs checks if specific outpoints exist in the token's overlay
// @Summary Validate outpoints (bulk)
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param outpoints body []string true "Array of outpoints to validate (max 1000)"
// @Success 200 {array} txo.IndexedOutputResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /{tokenId}/outputs [post]
func (r *Routes) ValidateOutputs(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	var outpointStrs []string
	if err := c.BodyParser(&outpointStrs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(outpointStrs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No outpoints provided",
		})
	}

	if len(outpointStrs) > 1000 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many outpoints (max 1000)",
		})
	}

	outpoints := make([]*transaction.Outpoint, 0, len(outpointStrs))
	for _, opStr := range outpointStrs {
		op, err := transaction.OutpointFromString(opStr)
		if err != nil {
			r.logger.Debug("invalid outpoint format", "outpoint", opStr, "error", err)
			continue
		}
		outpoints = append(outpoints, op)
	}

	if len(outpoints) == 0 {
		return c.JSON([]*txo.IndexedOutput{})
	}

	outputs, err := r.lookup.LoadOutputs(c.Context(), tokenId, outpoints)
	if err != nil {
		r.logger.Error("ValidateOutputs load error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// GetTokenOutput checks if a single outpoint exists in the token's overlay
// @Summary Validate outpoint
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param outpoint path string true "Outpoint (format: txid_vout or txid.vout)"
// @Success 200 {object} txo.IndexedOutputResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /{tokenId}/outputs/{outpoint} [get]
func (r *Routes) GetTokenOutput(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	outpointStr := c.Params("outpoint")

	op, err := transaction.OutpointFromString(outpointStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid outpoint format",
		})
	}

	outputs, err := r.lookup.LoadOutputs(c.Context(), tokenId, []*transaction.Outpoint{op})
	if err != nil {
		r.logger.Error("GetTokenOutput load error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	if len(outputs) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
			Message: "Outpoint not found in topic",
		})
	}

	return c.JSON(outputs[0])
}

// parseSearchConfig extracts search parameters from the request
func parseSearchConfig(c *fiber.Ctx) *store.SearchCfg {
	cfg := &store.SearchCfg{}

	if fromStr := c.Query("from"); fromStr != "" {
		if from, err := strconv.ParseFloat(fromStr, 64); err == nil {
			cfg.From = &from
		}
	}

	if toStr := c.Query("to"); toStr != "" {
		if to, err := strconv.ParseFloat(toStr, 64); err == nil {
			cfg.To = &to
		}
	}

	if limitStr := c.Query("limit"); limitStr != "" {
		if limit, err := strconv.ParseUint(limitStr, 10, 32); err == nil {
			cfg.Limit = uint32(limit)
		}
	}

	if c.Query("rev") == "true" {
		cfg.Reverse = true
	}

	return cfg
}
