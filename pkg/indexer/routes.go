package indexer

import (
	"encoding/hex"
	"fmt"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for the indexer
type Routes struct {
	ingestCtx *IngestCtx
	ps        pubsub.PubSub
}

// NewRoutes creates a new Routes instance
func NewRoutes(ingestCtx *IngestCtx, ps pubsub.PubSub) *Routes {
	return &Routes{
		ingestCtx: ingestCtx,
		ps:        ps,
	}
}

// Register registers the indexer routes with the Fiber app
func (r *Routes) Register(app fiber.Router, prefix string) {
	g := app.Group(prefix)

	g.Get("/parse/:txid", r.ParseTxidHandler)
	g.Post("/parse", r.ParseTxHandler)
	g.Post("/ingest/:txid", r.IngestTxidHandler)
	g.Get("/tags", r.TagsHandler)
}

// ParseTxidHandler handles GET /parse/:txid
// @Summary Parse a transaction by txid
// @Description Parses a transaction and returns the indexed context without saving
// @Tags indexer
// @Accept json
// @Produce json
// @Param txid path string true "Transaction ID"
// @Success 200 {object} IndexContext
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /indexer/parse/{txid} [get]
func (r *Routes) ParseTxidHandler(c *fiber.Ctx) error {
	txid := c.Params("txid")
	if txid == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "txid is required",
		})
	}

	idxCtx, err := r.ingestCtx.ParseTxid(c.Context(), txid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	if idxCtx == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "transaction not found",
		})
	}

	return c.JSON(idxCtx)
}

// ParseTxHandler handles POST /parse
// @Summary Parse a transaction from raw bytes
// @Description Parses a transaction from raw bytes and returns the indexed context without saving
// @Tags indexer
// @Accept application/octet-stream
// @Produce json
// @Param tx body []byte true "Raw transaction bytes"
// @Success 200 {object} IndexContext
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/indexer/parse [post]
func (r *Routes) ParseTxHandler(c *fiber.Ctx) error {
	body := c.Body()
	if len(body) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "request body is required",
		})
	}

	tx, err := transaction.NewTransactionFromBytes(body)
	if err != nil {
		// Try hex decoding
		decoded, hexErr := hex.DecodeString(string(body))
		if hexErr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("invalid transaction: %v", err),
			})
		}
		tx, err = transaction.NewTransactionFromBytes(decoded)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("invalid transaction: %v", err),
			})
		}
	}

	idxCtx, err := r.ingestCtx.ParseTx(c.Context(), tx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(idxCtx)
}

// IngestTxidHandler handles POST /ingest/:txid
// @Summary Ingest a transaction by txid
// @Description Ingests a transaction by txid, parsing and saving to the store
// @Tags indexer
// @Accept json
// @Produce json
// @Param txid path string true "Transaction ID"
// @Success 200 {object} IndexContext
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /indexer/ingest/{txid} [post]
func (r *Routes) IngestTxidHandler(c *fiber.Ctx) error {
	txid := c.Params("txid")
	if txid == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "txid is required",
		})
	}

	idxCtx, err := r.ingestCtx.IngestTxid(c.Context(), txid)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	if idxCtx == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "transaction not found or already ingested",
		})
	}

	// Publish events for each output
	if r.ps != nil {
		for _, output := range idxCtx.Outputs {
			outpoint := output.Outpoint.String()
			for _, event := range output.Events {
				r.ps.Publish(c.Context(), event, outpoint, idxCtx.Score)
			}
		}
	}

	return c.JSON(idxCtx)
}

// TagsHandler handles GET /tags
// @Summary Get available indexer tags
// @Description Returns the list of tags from all registered indexers
// @Tags indexer
// @Produce json
// @Success 200 {array} string
// @Router /api/indexer/tags [get]
func (r *Routes) TagsHandler(c *fiber.Ctx) error {
	return c.JSON(r.ingestCtx.IndexedTags())
}
