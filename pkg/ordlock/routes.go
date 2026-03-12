package ordlock

import (
	"database/sql"
	"log/slog"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

type Routes struct {
	lookup *lookup.OrdLockLookup
	topic  string
	logger *slog.Logger
}

func NewRoutes(lookup *lookup.OrdLockLookup, topic string, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		lookup: lookup,
		topic:  topic,
		logger: logger,
	}
}

func (r *Routes) Register(router fiber.Router) {
	router.Get("/listings", r.SearchListings)
	router.Get("/listing/:outpoint", r.GetListing)
}

// SearchListings searches for OrdLock listings.
// @Summary Search listings
// @Tags ordlock
// @Produce json
// @Param type query string false "Content type filter"
// @Param q query string false "Name search"
// @Param limit query int false "Results limit" default(20)
// @Param from query number false "Pagination score"
// @Param rev query bool false "Reverse order" default(true)
// @Success 200 {array} object
// @Failure 500 {object} object{message=string}
// @Router /ordlock/listings [get]
func (r *Routes) SearchListings(c *fiber.Ctx) error {
	contentType := c.Query("type")
	q := c.Query("q")
	limit := c.QueryInt("limit", 20)
	rev := c.Query("rev", "true") == "true"

	var from float64
	if fromStr := c.Query("from"); fromStr != "" {
		var err error
		from, err = strconv.ParseFloat(fromStr, 64)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid 'from' parameter",
			})
		}
	}

	results, err := r.lookup.SearchListings(c.Context(), r.topic, contentType, q, limit, from, rev)
	if err != nil {
		r.logger.Error("failed to search listings", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to search listings: " + err.Error(),
		})
	}

	return c.JSON(results)
}

// GetListing retrieves a single OrdLock listing by outpoint.
// @Summary Get listing
// @Tags ordlock
// @Produce json
// @Param outpoint path string true "Outpoint (txid.vout)"
// @Success 200 {object} object
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /ordlock/listing/{outpoint} [get]
func (r *Routes) GetListing(c *fiber.Ctx) error {
	op, err := transaction.OutpointFromString(c.Params("outpoint"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid outpoint format",
		})
	}

	result, err := r.lookup.GetListing(c.Context(), r.topic, op.Bytes())
	if err != nil {
		if err == sql.ErrNoRows {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"message": "Listing not found",
			})
		}
		r.logger.Error("failed to get listing", "error", err, "outpoint", c.Params("outpoint"))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to get listing: " + err.Error(),
		})
	}

	return c.JSON(result)
}
