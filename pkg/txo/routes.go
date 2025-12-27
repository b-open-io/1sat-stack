package txo

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for TXO queries.
type Routes struct {
	outputStore *OutputStore
}

// NewRoutes creates a new Routes instance.
func NewRoutes(outputStore *OutputStore) *Routes {
	return &Routes{outputStore: outputStore}
}

// Outpoint pattern: 64 hex chars + separator (. or _) + decimal digits
const outpointPattern = `[a-fA-F0-9]{64}[._][0-9]+`

// Register registers all TXO routes on the given router.
func (r *Routes) Register(router fiber.Router) {
	// Batch operations
	router.Post("/outpoints", r.GetTxos)
	router.Post("/spends", r.GetSpends)

	// By transaction
	router.Get("/tx/:txid", r.TxosByTxid)

	// Generic search
	router.Get("/search", r.Search)

	// Direct outpoint lookups (pattern-matched)
	router.Get("/:outpoint<regex("+outpointPattern+")>", r.GetTxo)
	router.Get("/:outpoint<regex("+outpointPattern+")>/spend", r.GetSpend)
}

// GetTxo returns a single TXO by outpoint.
// @Summary Get TXO by outpoint
// @Description Get a transaction output by its outpoint
// @Tags txos
// @Produce json
// @Param outpoint path string true "Outpoint in format txid_vout or txid:vout"
// @Param tags query string false "Comma-separated list of tags to include"
// @Param spend query bool false "Include spend txid" default(true)
// @Success 200 {object} IndexedOutputResponse
// @Failure 400 {string} string "Invalid outpoint format"
// @Failure 404 {string} string "TXO not found"
// @Failure 500 {string} string "Internal server error"
// @Router /txo/{outpoint} [get]
func (r *Routes) GetTxo(c *fiber.Ctx) error {
	op, err := ParseOutpoint(c.Params("outpoint"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid outpoint format")
	}

	cfg := &OutputSearchCfg{
		IncludeSpend: c.QueryBool("spend", true),
	}
	if tagsQuery := c.Query("tags", ""); tagsQuery != "" {
		cfg.IncludeTags = strings.Split(tagsQuery, ",")
	}

	output, err := r.outputStore.LoadOutput(c.Context(), op, cfg)
	if err != nil {
		return err
	}
	if output == nil {
		return c.Status(fiber.StatusNotFound).SendString("TXO not found")
	}

	return c.JSON(output)
}

// GetSpend returns the spend information for an outpoint.
// @Summary Get spend info for outpoint
// @Description Get the spending transaction for an outpoint
// @Tags txos
// @Produce json
// @Param outpoint path string true "Outpoint in format txid_vout or txid:vout"
// @Success 200 {object} SpendResponse
// @Failure 400 {string} string "Invalid outpoint format"
// @Failure 500 {string} string "Internal server error"
// @Router /txo/{outpoint}/spend [get]
func (r *Routes) GetSpend(c *fiber.Ctx) error {
	op, err := ParseOutpoint(c.Params("outpoint"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid outpoint format")
	}

	spendTxid, err := r.outputStore.GetSpend(c.Context(), op)
	if err != nil {
		return err
	}

	resp := SpendResponse{}
	if spendTxid != nil {
		txidStr := spendTxid.String()
		resp.SpendTxid = &txidStr
	}

	return c.JSON(resp)
}

// SpendResponse is the response for spend queries.
type SpendResponse struct {
	SpendTxid *string `json:"spendTxid"`
}

// GetTxos returns multiple TXOs by outpoints.
// @Summary Get multiple TXOs
// @Description Get multiple transaction outputs by their outpoints
// @Tags txos
// @Accept json
// @Produce json
// @Param outpoints body []string true "Array of outpoints"
// @Param tags query string false "Comma-separated list of tags to include"
// @Param spend query bool false "Include spend txid" default(true)
// @Success 200 {array} IndexedOutputResponse
// @Failure 500 {string} string "Internal server error"
// @Router /txo/outpoints [post]
func (r *Routes) GetTxos(c *fiber.Ctx) error {
	var outpoints []string
	if err := c.BodyParser(&outpoints); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
	}

	cfg := &OutputSearchCfg{
		IncludeSpend: c.QueryBool("spend", true),
	}
	if tagsQuery := c.Query("tags", ""); tagsQuery != "" {
		cfg.IncludeTags = strings.Split(tagsQuery, ",")
	}

	outputs := make([]*IndexedOutput, len(outpoints))
	for i, opStr := range outpoints {
		op, err := ParseOutpoint(opStr)
		if err != nil {
			continue
		}
		output, err := r.outputStore.LoadOutput(c.Context(), op, cfg)
		if err != nil {
			return err
		}
		outputs[i] = output
	}

	return c.JSON(outputs)
}

// GetSpends returns spend information for multiple outpoints.
// @Summary Get spends for multiple outpoints
// @Description Get spending transactions for multiple outpoints
// @Tags txos
// @Accept json
// @Produce json
// @Param outpoints body []string true "Array of outpoints"
// @Success 200 {array} SpendResponse
// @Failure 500 {string} string "Internal server error"
// @Router /txo/spends [post]
func (r *Routes) GetSpends(c *fiber.Ctx) error {
	var outpoints []string
	if err := c.BodyParser(&outpoints); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
	}

	ops := make([]*transaction.Outpoint, len(outpoints))
	for i, opStr := range outpoints {
		op, err := ParseOutpoint(opStr)
		if err != nil {
			continue
		}
		ops[i] = op
	}

	spends, err := r.outputStore.GetSpends(c.Context(), ops)
	if err != nil {
		return err
	}

	responses := make([]SpendResponse, len(spends))
	for i, spend := range spends {
		if spend != nil {
			txidStr := spend.String()
			responses[i].SpendTxid = &txidStr
		}
	}

	return c.JSON(responses)
}

// TxosByTxid returns all TXOs for a transaction.
// @Summary Get TXOs by transaction ID
// @Description Get all transaction outputs for a specific transaction
// @Tags txos
// @Produce json
// @Param txid path string true "Transaction ID"
// @Param tags query string false "Comma-separated list of tags to include"
// @Param spend query bool false "Include spend txid" default(true)
// @Success 200 {array} IndexedOutputResponse
// @Failure 400 {string} string "Invalid txid"
// @Failure 500 {string} string "Internal server error"
// @Router /txo/tx/{txid} [get]
func (r *Routes) TxosByTxid(c *fiber.Ctx) error {
	txidStr := c.Params("txid")

	txid, err := ParseTxid(txidStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid txid")
	}

	cfg := &OutputSearchCfg{
		IncludeSpend: c.QueryBool("spend", true),
	}
	if tagsQuery := c.Query("tags", ""); tagsQuery != "" {
		cfg.IncludeTags = strings.Split(tagsQuery, ",")
	}

	outputs, err := r.outputStore.LoadOutputsByTxid(c.Context(), txid, cfg)
	if err != nil {
		return err
	}

	return c.JSON(outputs)
}

// Search searches outputs by one or more keys.
// @Summary Search outputs by key(s)
// @Description Search transaction outputs by indexed keys. Keys use type prefixes: "ev:" for events, "tp:" for topics. Without prefix, "ev:" is assumed.
// @Tags search
// @Produce json
// @Param key query []string true "Search key(s) (e.g., ev:own:address, tp:tm_bsv21, own:address)"
// @Param from query number false "Starting score for pagination"
// @Param rev query bool false "Reverse order"
// @Param limit query int false "Maximum number of results" default(100)
// @Param unspent query bool false "Filter for unspent outputs only"
// @Param sats query bool false "Include satoshis"
// @Param spend query bool false "Include spend txid"
// @Param events query bool false "Include events array"
// @Param block query bool false "Include blockHeight and blockIdx"
// @Param tags query string false "Comma-separated list of data tags to include"
// @Success 200 {array} IndexedOutputResponse
// @Failure 400 {string} string "At least one key is required"
// @Failure 500 {string} string "Internal server error"
// @Router /txo/search [get]
func (r *Routes) Search(c *fiber.Ctx) error {
	keys := c.Context().QueryArgs().PeekMulti("key")
	if len(keys) == 0 {
		return c.Status(fiber.StatusBadRequest).SendString("At least one key is required")
	}

	cfg := &OutputSearchCfg{
		FilterSpent:   c.QueryBool("unspent", false),
		IncludeSats:   c.QueryBool("sats", false),
		IncludeSpend:  c.QueryBool("spend", false),
		IncludeEvents: c.QueryBool("events", false),
		IncludeBlock:  c.QueryBool("block", false),
	}

	cfg.Keys = make([][]byte, len(keys))
	for i, k := range keys {
		cfg.Keys[i] = k
	}
	cfg.Limit = uint32(c.QueryInt("limit", 100))
	cfg.Reverse = c.QueryBool("rev", false)

	if tagsQuery := c.Query("tags", ""); tagsQuery != "" {
		cfg.IncludeTags = strings.Split(tagsQuery, ",")
	}

	if from := c.QueryFloat("from", 0); from != 0 {
		cfg.From = &from
	}

	outputs, err := r.outputStore.SearchOutputs(c.Context(), cfg)
	if err != nil {
		return err
	}

	return c.JSON(outputs)
}

// === Helper functions ===

// Outpoint is an alias for transaction.Outpoint
type Outpoint = transaction.Outpoint

// ParseOutpoint parses an outpoint string in format "txid_vout" or "txid:vout"
func ParseOutpoint(s string) (*Outpoint, error) {
	// Try underscore separator first, then colon
	var parts []string
	if strings.Contains(s, "_") {
		parts = strings.Split(s, "_")
	} else if strings.Contains(s, ":") {
		parts = strings.Split(s, ":")
	} else {
		return nil, fmt.Errorf("invalid outpoint format: %s", s)
	}

	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid outpoint format: %s", s)
	}

	txid, err := ParseTxid(parts[0])
	if err != nil {
		return nil, err
	}

	vout, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid vout: %w", err)
	}

	return &transaction.Outpoint{
		Txid:  *txid,
		Index: uint32(vout),
	}, nil
}

// ParseTxid parses a transaction ID hex string
func ParseTxid(s string) (*chainhash.Hash, error) {
	if len(s) != 64 {
		return nil, fmt.Errorf("invalid txid length: %d", len(s))
	}

	bytes, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid txid hex: %w", err)
	}

	// Reverse bytes (txid is displayed in reverse byte order)
	for i, j := 0, len(bytes)-1; i < j; i, j = i+1, j-1 {
		bytes[i], bytes[j] = bytes[j], bytes[i]
	}

	txid := &chainhash.Hash{}
	copy(txid[:], bytes)
	return txid, nil
}
