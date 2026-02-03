package bsv21

import (
	"log/slog"
	"strconv"

	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for BSV21 API
type Routes struct {
	storage      *txo.OutputStore
	lookup       *lookuppkg.BSV21Lookup
	manager      *TokenManager
	chaintracker chaintracks.Chaintracks
	logger       *slog.Logger
}

// RoutesDeps holds dependencies for BSV21 routes
type RoutesDeps struct {
	Storage      *txo.OutputStore
	Lookup       *lookuppkg.BSV21Lookup
	Manager      *TokenManager
	ChainTracker chaintracks.Chaintracks
	Logger       *slog.Logger
}

// NewRoutes creates a new Routes instance
func NewRoutes(cfg *RoutesDeps) *Routes {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		storage:      cfg.Storage,
		lookup:       cfg.Lookup,
		manager:      cfg.Manager,
		chaintracker: cfg.ChainTracker,
		logger:       logger,
	}
}

// Register registers the BSV21 routes with the Fiber router
func (r *Routes) Register(router fiber.Router) {
	// Static routes must be registered before parameterized routes
	router.Post("/lookup", r.LookupTokens)

	router.Get("/:tokenId", r.GetToken)
	router.Get("/:tokenId/blk/:height", r.GetBlockData)
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
	TxID        string        `json:"txid"`
	Inputs      []*OutputData `json:"inputs"`
	Outputs     []*OutputData `json:"outputs"`
	Beef        []byte        `json:"beef,omitempty"`
	BlockHeight uint32        `json:"block_height,omitempty"`
}

// BlockResponse represents block data for a token
type BlockResponse struct {
	Block        BlockInfo          `json:"block"`
	Transactions []*TransactionData `json:"transactions"`
}

// BlockInfo represents block header information
type BlockInfo struct {
	Height            uint32 `json:"height"`
	Hash              string `json:"hash"`
	PreviousBlockHash string `json:"previousblockhash"`
	Timestamp         uint32 `json:"timestamp,omitempty"`
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

// GetToken retrieves BSV21 token details and funding status
// @Summary Get BSV21 token details with funding status
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Success 200 {object} TokenDetailResponse
// @Router /bsv21/{tokenId} [get]
func (r *Routes) GetToken(c *fiber.Ctx) error {
	tokenIdStr := c.Params("tokenId")

	resp, err := r.getTokenDetail(c, tokenIdStr)
	if err != nil {
		return err
	}

	return c.JSON(resp)
}

// LookupTokens retrieves details for multiple tokens
// @Summary Bulk lookup BSV21 token details with funding status
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenIds body []string true "Array of token IDs (max 100)"
// @Success 200 {array} TokenDetailResponse
// @Router /bsv21/lookup [post]
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

// getTokenDetail loads combined token data and funding status for a single token ID
func (r *Routes) getTokenDetail(c *fiber.Ctx, tokenIdStr string) (*TokenDetailResponse, error) {
	outpoint, err := transaction.OutpointFromString(tokenIdStr)
	if err != nil {
		return nil, c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid token ID format: " + tokenIdStr,
		})
	}

	tokenData, err := r.lookup.GetToken(c.Context(), outpoint)
	if err != nil {
		return nil, c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
			Message: "Token not found: " + tokenIdStr,
		})
	}

	resp := &TokenDetailResponse{
		TokenID: tokenIdStr,
		Token:   tokenData,
	}

	// Include funding status if the token manager is available
	if r.manager != nil {
		status, err := r.manager.GetTokenStatus(c.Context(), tokenIdStr)
		if err == nil {
			resp.Status = status
		}
	}

	return resp, nil
}

// GetBlockData retrieves block data for a token at a specific height
// @Summary Get block data for a token
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param height path int true "Block height"
// @Success 200 {object} BlockResponse
// @Router /bsv21/{tokenId}/blk/{height} [get]
func (r *Routes) GetBlockData(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	heightStr := c.Params("height")

	height64, err := strconv.ParseUint(heightStr, 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid height parameter",
		})
	}
	height := uint32(height64)

	// Search for outputs at this height in the topic
	score := types.HeightScore(height, 0)
	scoreEnd := types.HeightScore(height+1, 0)

	cfg := &txo.OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys: [][]byte{[]byte("tp:tm_" + tokenId)},
			From: &score,
			To:   &scoreEnd,
		},
		IncludeSpend: true,
		IncludeTags:  []string{"bsv21"},
	}

	outputs, err := r.storage.SearchOutputs(c.Context(), cfg)
	if err != nil {
		r.logger.Error("GetBlockData error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to get block data",
		})
	}

	// Group outputs by transaction
	txMap := make(map[string]*TransactionData)
	for _, output := range outputs {
		if output == nil {
			continue
		}
		txidStr := output.Outpoint.Txid.String()
		if _, exists := txMap[txidStr]; !exists {
			txMap[txidStr] = &TransactionData{
				TxID:        txidStr,
				BlockHeight: height,
			}
		}
		od := &OutputData{
			Vout: output.Outpoint.Index,
			Data: output.Data,
		}
		if output.SpendTxid != nil {
			s := output.SpendTxid.String()
			od.Spend = &s
		}
		txMap[txidStr].Outputs = append(txMap[txidStr].Outputs, od)
	}

	transactions := make([]*TransactionData, 0, len(txMap))
	for _, tx := range txMap {
		transactions = append(transactions, tx)
	}

	blockHeader, err := r.chaintracker.GetHeaderByHeight(c.UserContext(), height)
	if err != nil {
		return c.JSON(BlockResponse{
			Block: BlockInfo{
				Height: height,
			},
			Transactions: transactions,
		})
	}

	return c.JSON(BlockResponse{
		Block: BlockInfo{
			Height:            blockHeader.Height,
			Hash:              blockHeader.Hash.String(),
			PreviousBlockHash: blockHeader.Header.PrevHash.String(),
			Timestamp:         blockHeader.Header.Timestamp,
		},
		Transactions: transactions,
	})
}

// GetTransaction retrieves transaction details for a token
// @Summary Get transaction details
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param txid path string true "Transaction ID"
// @Param beef query bool false "Include BEEF data"
// @Success 200 {object} TransactionData
// @Router /bsv21/{tokenId}/tx/{txid} [get]
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
	topic := "tm_" + tokenId

	// Query txid event index for outputs of this transaction
	cfg := &txo.OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys: [][]byte{txo.KeyEvent("txid:" + txidStr)},
		},
		IncludeSpend: true,
		IncludeBlock: true,
		IncludeTags:  []string{"bsv21"},
	}

	rawOutputs, err := r.storage.SearchOutputs(c.Context(), cfg)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve transaction details",
		})
	}

	// Filter to BSV21 outputs and convert to OutputData
	var outputs []*OutputData
	var blockHeight uint32
	var firstOutput *txo.IndexedOutput
	for _, out := range rawOutputs {
		if out == nil || out.Data == nil {
			continue
		}
		if _, ok := out.Data["bsv21"]; !ok {
			continue
		}
		od := &OutputData{
			Vout: out.Outpoint.Index,
			Data: out.Data,
		}
		if out.SpendTxid != nil {
			s := out.SpendTxid.String()
			od.Spend = &s
		}
		outputs = append(outputs, od)
		if firstOutput == nil {
			firstOutput = out
			if out.BlockHeight != nil {
				blockHeight = *out.BlockHeight
			}
		}
	}

	if len(outputs) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
			Message: "Transaction not found",
		})
	}

	// Find inputs: outputs from other transactions that were consumed by this transaction.
	// GetInputsConsumed returns the outpoints consumed to produce a given output for a topic.
	// All outputs of the same tx share the same consumed inputs, so query the first one.
	var inputs []*OutputData
	consumedOps, err := r.storage.GetInputsConsumed(c.Context(), &firstOutput.Outpoint, topic)
	if err == nil && len(consumedOps) > 0 {
		// Load the consumed outputs with their BSV21 data
		inputOutputs, err := r.storage.LoadOutputs(c.Context(), consumedOps, &txo.OutputSearchCfg{
			IncludeTags: []string{"bsv21"},
		})
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
		TxID:        txidStr,
		Inputs:      inputs,
		Outputs:     outputs,
		BlockHeight: blockHeight,
	}

	if includeBeef && r.storage.BeefStore != nil {
		beef, err := r.storage.BeefStore.LoadBeef(c.Context(), txid)
		if err == nil && beef != nil {
			tx.Beef, _ = beef.AtomicBytes(txid)
		}
	}

	return c.JSON(tx)
}

// GetAddressBalance retrieves token balance for a single address
// @Summary Get address token balance
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param address path string true "Address"
// @Success 200 {object} BalanceResponse
// @Router /bsv21/{tokenId}/{lockType}/{address}/balance [get]
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
// @Summary Get address transaction history
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param address path string true "Address"
// @Success 200 {array} txo.IndexedOutput
// @Router /bsv21/{tokenId}/{lockType}/{address}/history [get]
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

	outputs, err := r.lookup.LoadOutputs(c.Context(), outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// GetAddressUnspent retrieves unspent outputs for a single address
// @Summary Get address unspent outputs
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param address path string true "Address"
// @Success 200 {array} txo.IndexedOutput
// @Router /bsv21/{tokenId}/{lockType}/{address}/unspent [get]
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

	outputs, err := r.lookup.LoadOutputs(c.Context(), outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// GetMultiAddressBalance retrieves token balance for multiple addresses
// @Summary Get multi-address token balance
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {object} BalanceResponse
// @Router /bsv21/{tokenId}/{lockType}/balance [post]
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
// @Summary Get multi-address transaction history
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {array} txo.IndexedOutput
// @Router /bsv21/{tokenId}/{lockType}/history [post]
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

	outputs, err := r.lookup.LoadOutputs(c.Context(), outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
}

// GetMultiAddressUnspent retrieves unspent outputs for multiple addresses
// @Summary Get multi-address unspent outputs
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID"
// @Param lockType path string true "Lock type"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {array} txo.IndexedOutput
// @Router /bsv21/{tokenId}/{lockType}/unspent [post]
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

	outputs, err := r.lookup.LoadOutputs(c.Context(), outpoints)
	if err != nil {
		r.logger.Error("Failed to load outputs", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to load output data",
		})
	}

	return c.JSON(outputs)
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
