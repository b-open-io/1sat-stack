package txo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for TXO queries.
//
// Spends is the public spend resolver used by the /spend and /spends API
// handlers — it delegates through the configured provider chain (local
// store, JungleBus fallback, etc.) so external callers see spends recorded
// by upstream sources even when the local index hasn't ingested them yet.
// When nil, the handlers fall back to OutputStore's local-only path.
type Routes struct {
	outputStore         *OutputStore
	Spends              *spends.Storage
	CollectionOwnership CollectionOwnershipResolver
}

// CollectionOwnershipResolver follows an ordinal root's ownership history.
type CollectionOwnershipResolver interface {
	OwnershipChain(ctx context.Context, root *transaction.Outpoint) ([]ordfs.OwnershipEntry, error)
}

// NewRoutes creates a new Routes instance. Set Spends after construction
// to enable the provider-chain spend lookup for the public API handlers;
// without it, /spend and /spends only see locally-indexed data.
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

	// Collection membership
	router.Get("/collections/:collectionId", r.CollectionMembers)

	// Direct outpoint lookups (pattern-matched)
	router.Get("/:outpoint<regex("+outpointPattern+")>", r.GetTxo)
	router.Get("/:outpoint<regex("+outpointPattern+")>/spend", r.GetSpend)
}

// CollectionMemberResponse is an authoritative collection member with the
// small MAP subset needed by collection UIs.
type CollectionMemberResponse struct {
	Outpoint string         `json:"outpoint"`
	Map      map[string]any `json:"map,omitempty"`
}

// CollectionMembers returns members signed by the controller of the
// collection root at each member's inscription block position.
// @Summary List authoritative collection members
// @Description Returns signed collectionItem outputs, including spent origins, whose signer controlled the collection root when the item was inscribed.
// @Tags txos
// @Produce json
// @Param collectionId path string true "Collection root outpoint"
// @Param limit query int false "Maximum candidates to inspect" default(100)
// @Param rev query bool false "Reverse order"
// @Success 200 {array} CollectionMemberResponse
// @Failure 400 {string} string "Invalid collection ID"
// @Failure 404 {string} string "Collection root not found"
// @Failure 500 {string} string "Internal server error"
// @Router /collections/{collectionId} [get]
func (r *Routes) CollectionMembers(c *fiber.Ctx) error {
	collectionRoot, err := transaction.OutpointFromString(c.Params("collectionId"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid collection ID")
	}
	collectionID := collectionRoot.OrdinalString()

	root, err := r.outputStore.LoadOutput(c.Context(), collectionRoot, &OutputSearchCfg{
		IncludeSats:  true,
		IncludeBlock: true,
		IncludeTags:  []string{parse.TagMAP},
	})
	if err != nil {
		return err
	}
	if root == nil {
		return c.Status(fiber.StatusNotFound).SendString("Collection root not found")
	}
	rootMap, ok := outputMAPData(root)
	if !ok || rootMap["subType"] != "collection" || root.Satoshis == nil || *root.Satoshis != 1 {
		return c.Status(fiber.StatusNotFound).SendString("Collection root not found")
	}
	if r.CollectionOwnership == nil {
		return fmt.Errorf("collection ownership resolver is not configured")
	}

	ownership, err := r.CollectionOwnership.OwnershipChain(c.Context(), collectionRoot)
	if err != nil {
		return err
	}
	if len(ownership) == 0 || ownership[0].Address == "" {
		return c.JSON([]CollectionMemberResponse{})
	}

	ownershipOutpoints := make([]*transaction.Outpoint, len(ownership))
	for i := range ownership {
		ownershipOutpoints[i] = &ownership[i].Outpoint
	}
	ownershipOutputs, err := r.outputStore.LoadOutputs(c.Context(), ownershipOutpoints, &OutputSearchCfg{
		IncludeBlock: true,
	})
	if err != nil {
		return err
	}

	cfg := &OutputSearchCfg{
		IncludeEvents: true,
		IncludeBlock:  true,
		IncludeTags:   []string{parse.TagMAP},
	}
	cfg.Keys = [][]byte{[]byte("ev:map:collectionId:" + collectionID)}
	cfg.Limit = uint32(c.QueryInt("limit", 100))
	cfg.Reverse = c.QueryBool("rev", false)

	results, err := r.outputStore.Search(c.Context(), cfg)
	if err != nil {
		return err
	}
	candidates, err := r.outputStore.LoadOutputsFromResults(c.Context(), results, cfg)
	if err != nil {
		return err
	}

	members := make([]CollectionMemberResponse, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		mapData, ok := outputMAPData(candidate)
		if !ok || mapData["subType"] != "collectionItem" {
			continue
		}

		controller := controllingAddressAt(candidate, ownership, ownershipOutputs)
		if controller == "" || !hasSigner(candidate.Events, controller) {
			continue
		}

		memberMap := make(map[string]any, 2)
		if subTypeData, ok := mapData["subTypeData"]; ok {
			var metadata map[string]any
			if json.Unmarshal([]byte(subTypeData), &metadata) == nil {
				if mintNumber, ok := metadata["mintNumber"]; ok {
					memberMap["mintNumber"] = mintNumber
				}
				if rarityLabel, ok := metadata["rarityLabel"]; ok {
					memberMap["rarityLabel"] = rarityLabel
				}
			}
		}

		members = append(members, CollectionMemberResponse{
			Outpoint: candidate.Outpoint.OrdinalString(),
			Map:      memberMap,
		})
	}

	return c.JSON(members)
}

func outputMAPData(output *IndexedOutput) (map[string]string, bool) {
	if output == nil || output.Data == nil {
		return nil, false
	}
	raw, ok := output.Data[parse.TagMAP]
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var decoded struct {
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal(encoded, &decoded) != nil || decoded.Data == nil {
		return nil, false
	}
	return decoded.Data, true
}

func controllingAddressAt(candidate *IndexedOutput, ownership []ordfs.OwnershipEntry, outputs []*IndexedOutput) string {
	var controller string
	for i, entry := range ownership {
		if i >= len(outputs) || outputs[i] == nil || ownershipAfterCandidate(outputs[i], candidate) {
			break
		}
		controller = entry.Address
	}
	return controller
}

func ownershipAfterCandidate(ownership, candidate *IndexedOutput) bool {
	if candidate == nil || candidate.BlockHeight == nil || *candidate.BlockHeight == 0 {
		return false
	}
	if ownership == nil || ownership.BlockHeight == nil || *ownership.BlockHeight == 0 {
		return true
	}
	if *ownership.BlockHeight != *candidate.BlockHeight {
		return *ownership.BlockHeight > *candidate.BlockHeight
	}

	var ownershipIdx, candidateIdx uint64
	if ownership.BlockIdx != nil {
		ownershipIdx = *ownership.BlockIdx
	}
	if candidate.BlockIdx != nil {
		candidateIdx = *candidate.BlockIdx
	}
	return ownershipIdx > candidateIdx
}

func hasSigner(events []string, address string) bool {
	want := "signer:" + address
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

// GetTxo returns a single TXO by outpoint.
// @Summary Get TXO by outpoint
// @Description Get a transaction output by its outpoint
// @Tags txos
// @Produce json
// @Param outpoint path string true "Outpoint in format txid_vout or txid.vout"
// @Param tags query string false "Comma-separated list of tags to include"
// @Param spend query bool false "Include spend txid" default(true)
// @Success 200 {object} IndexedOutputResponse
// @Failure 400 {string} string "Invalid outpoint format"
// @Failure 404 {string} string "TXO not found"
// @Failure 500 {string} string "Internal server error"
// @Router /{outpoint} [get]
func (r *Routes) GetTxo(c *fiber.Ctx) error {
	op, err := transaction.OutpointFromString(c.Params("outpoint"))
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
// @Param outpoint path string true "Outpoint in format txid_vout or txid.vout"
// @Success 200 {object} SpendResponse
// @Failure 400 {string} string "Invalid outpoint format"
// @Failure 500 {string} string "Internal server error"
// @Router /{outpoint}/spend [get]
func (r *Routes) GetSpend(c *fiber.Ctx) error {
	op, err := transaction.OutpointFromString(c.Params("outpoint"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid outpoint format")
	}

	var spendTxid *chainhash.Hash
	if r.Spends != nil {
		spendTxid, err = r.Spends.GetSpend(c.Context(), op)
	} else {
		spendTxid, err = r.outputStore.GetSpend(c.Context(), op)
	}
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
// @Router /outpoints [post]
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
		op, err := transaction.OutpointFromString(opStr)
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
// @Router /spends [post]
func (r *Routes) GetSpends(c *fiber.Ctx) error {
	var outpoints []string
	if err := c.BodyParser(&outpoints); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
	}

	ops := make([]*transaction.Outpoint, len(outpoints))
	for i, opStr := range outpoints {
		op, err := transaction.OutpointFromString(opStr)
		if err != nil {
			continue
		}
		ops[i] = op
	}

	var spendList []*chainhash.Hash
	var err error
	if r.Spends != nil {
		spendList, err = r.Spends.GetSpends(c.Context(), ops)
	} else {
		spendList, err = r.outputStore.GetSpends(c.Context(), ops)
	}
	if err != nil {
		return err
	}

	responses := make([]SpendResponse, len(spendList))
	for i, spend := range spendList {
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
// @Router /tx/{txid} [get]
func (r *Routes) TxosByTxid(c *fiber.Ctx) error {
	txidStr := c.Params("txid")

	txid, err := chainhash.NewHashFromHex(txidStr)
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
// @Tags txos
// @Produce json
// @Param key query []string true "Search key(s) (e.g., ev:own:address, tp:tm_bsv21, own:address)"
// @Param join query string false "Join type for multiple keys: union (default), intersect, difference"
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
// @Router /search [get]
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

	switch c.Query("join", "") {
	case "intersect":
		cfg.JoinType = store.JoinIntersect
	case "difference":
		cfg.JoinType = store.JoinDifference
	}

	if tagsQuery := c.Query("tags", ""); tagsQuery != "" {
		cfg.IncludeTags = strings.Split(tagsQuery, ",")
	}

	if from := c.QueryFloat("from", 0); from != 0 {
		cfg.From = &from
	}

	results, err := r.outputStore.Search(c.Context(), cfg)
	if err != nil {
		return err
	}

	outputs, err := r.outputStore.LoadOutputsFromResults(c.Context(), results, cfg)
	if err != nil {
		return err
	}

	return c.JSON(outputs)
}

// === Helper functions ===

// Outpoint is an alias for transaction.Outpoint
type Outpoint = transaction.Outpoint
