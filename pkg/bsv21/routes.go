package bsv21

import (
	"fmt"
	"log/slog"
	"strconv"

	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
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
	chaintracker chaintracks.Chaintracks
	logger       *slog.Logger
}

// RoutesDeps holds dependencies for BSV21 routes
type RoutesDeps struct {
	Storage      *txo.OutputStore
	Lookup       *lookuppkg.BSV21Lookup
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
		chaintracker: cfg.ChainTracker,
		logger:       logger,
	}
}

// Register registers the BSV21 routes with the Fiber app
func (r *Routes) Register(app fiber.Router, prefix string) {
	g := app.Group(prefix)

	g.Get("/:tokenId", r.GetToken)
	g.Get("/:tokenId/blk/:height", r.GetBlockData)
	g.Get("/:tokenId/tx/:txid", r.GetTransaction)
	g.Get("/:tokenId/:lockType/:address/balance", r.GetAddressBalance)
	g.Get("/:tokenId/:lockType/:address/history", r.GetAddressHistory)
	g.Get("/:tokenId/:lockType/:address/unspent", r.GetAddressUnspent)
	g.Post("/:tokenId/:lockType/balance", r.GetMultiAddressBalance)
	g.Post("/:tokenId/:lockType/history", r.GetMultiAddressHistory)
	g.Post("/:tokenId/:lockType/unspent", r.GetMultiAddressUnspent)
}

// TokenResponse represents BSV21 token details
type TokenResponse struct {
	ID     string `json:"id"`
	Txid   string `json:"txid"`
	Vout   uint32 `json:"vout"`
	Op     string `json:"op"`
	Amt    string `json:"amt"`
	Symbol string `json:"sym,omitempty"`
	Dec    uint8  `json:"dec,omitempty"`
	Icon   string `json:"icon,omitempty"`
}

// TransactionData represents a transaction with its outputs
type TransactionData struct {
	TxID        string               `json:"txid"`
	Outputs     []*txo.IndexedOutput `json:"outputs"`
	Beef        []byte               `json:"beef,omitempty"`
	BlockHeight uint32               `json:"block_height,omitempty"`
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

// GetToken retrieves BSV21 token details
// @Summary Get BSV21 token details
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Success 200 {object} TokenResponse
// @Router /bsv21/{tokenId} [get]
func (r *Routes) GetToken(c *fiber.Ctx) error {
	tokenIdStr := c.Params("tokenId")

	outpoint, err := transaction.OutpointFromString(tokenIdStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid token ID format",
		})
	}

	tokenData, err := r.lookup.GetToken(c.Context(), outpoint)
	if err != nil {
		if err.Error() == "token not found" {
			return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
				Message: "Token not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve token details",
		})
	}

	return c.JSON(tokenData)
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

	cfg := txo.NewOutputSearchCfg().
		WithStringKeys("tp:tm_"+tokenId).
		WithRange(&score, &scoreEnd)

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
		txMap[txidStr].Outputs = append(txMap[txidStr].Outputs, output)
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
// @Success 200 {object} map[string]interface{}
// @Router /bsv21/{tokenId}/tx/{txid} [get]
func (r *Routes) GetTransaction(c *fiber.Ctx) error {
	txidStr := c.Params("txid")

	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid transaction ID format",
		})
	}

	includeBeef := c.Query("beef") == "true"

	// Load outputs for this txid
	outputs, err := r.storage.LoadOutputsByTxid(c.Context(), txid, nil)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve transaction details",
		})
	}

	if len(outputs) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
			Message: "Transaction not found",
		})
	}

	tx := &TransactionData{
		TxID:    txidStr,
		Outputs: outputs,
	}

	if len(outputs) > 0 && outputs[0] != nil {
		tx.BlockHeight = outputs[0].BlockHeight
	}

	if includeBeef && r.storage.BeefStore != nil {
		beef, err := r.storage.BeefStore.LoadBeef(c.Context(), txid)
		if err != nil {
			r.logger.Error("Failed to load BEEF", "txid", txid.String(), "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
				Message: "Failed to load BEEF data",
			})
		}
		tx.Beef = beef
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

	event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)

	balance, outputs, err := r.lookup.GetBalance(c.Context(), []string{event})
	if err != nil {
		r.logger.Error("Balance calculation error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to calculate balance",
		})
	}

	return c.JSON(BalanceResponse{
		Balance:   balance,
		UtxoCount: outputs,
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

	event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	cfg := parseOutputSearchConfig(c)
	cfg.Keys = [][]byte{[]byte(event)}

	outputs, err := r.storage.SearchOutputs(c.Context(), cfg)
	if err != nil {
		r.logger.Error("History lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve output history",
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

	event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	cfg := txo.NewOutputSearchCfg().
		WithStringKeys(event).
		WithFilterSpent(true)

	outputs, err := r.storage.SearchOutputs(c.Context(), cfg)
	if err != nil {
		r.logger.Error("Unspent lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve unspent outputs",
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

	events := make([]string, len(addresses))
	for i, address := range addresses {
		events[i] = fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	}

	balance, utxoCount, err := r.lookup.GetBalance(c.Context(), events)
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

	events := make([]string, len(addresses))
	for i, address := range addresses {
		events[i] = fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	}

	cfg := parseOutputSearchConfig(c)
	cfg.Keys = make([][]byte, len(events))
	for i, e := range events {
		cfg.Keys[i] = []byte(e)
	}

	outputs, err := r.storage.SearchOutputs(c.Context(), cfg)
	if err != nil {
		r.logger.Error("History lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve output history",
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

	events := make([]string, len(addresses))
	for i, address := range addresses {
		events[i] = fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	}

	cfg := txo.NewOutputSearchCfg().
		WithStringKeys(events...).
		WithFilterSpent(true)

	outputs, err := r.storage.SearchOutputs(c.Context(), cfg)
	if err != nil {
		r.logger.Error("Unspent lookup error", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve unspent outputs",
		})
	}

	return c.JSON(outputs)
}

// parseOutputSearchConfig extracts search parameters from the request
func parseOutputSearchConfig(c *fiber.Ctx) *txo.OutputSearchCfg {
	cfg := txo.NewOutputSearchCfg()

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
