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
	router.Get("/origin/:origin", r.GetListingByOrigin)
	router.Post("/origins", r.GetListingsByOrigins)
}

// SearchListings searches for OrdLock listings.
// @Summary Search listings
// @Tags market
// @Produce json
// @Param status query string false "Listing status: active, sale, cancel" default(active)
// @Param type query string false "Content type filter"
// @Param q query string false "Name search"
// @Param limit query int false "Results limit" default(20)
// @Param from query number false "Pagination score"
// @Param rev query bool false "Reverse order" default(true)
// @Success 200 {array} object
// @Failure 500 {object} object{message=string}
// @Router /market/listings [get]
func (r *Routes) SearchListings(c *fiber.Ctx) error {
	status := c.Query("status", "active")
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

	results, err := r.lookup.SearchListings(c.Context(), r.topic, status, contentType, q, limit, from, rev)
	if err != nil {
		r.logger.Error("failed to search listings", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to search listings: " + err.Error(),
		})
	}

	return c.JSON(results)
}

// GetListingByOrigin retrieves the active listing for an origin.
// @Summary Get active listing by origin
// @Tags market
// @Produce json
// @Param origin path string true "Origin (txid_vout or txid.vout)"
// @Success 200 {object} object
// @Failure 400 {object} object{message=string}
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /market/origin/{origin} [get]
func (r *Routes) GetListingByOrigin(c *fiber.Ctx) error {
	origin, err := transaction.OutpointFromString(c.Params("origin"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid origin format",
		})
	}
	result, err := r.lookup.GetListingByOrigin(c.Context(), r.topic, origin)
	if err != nil {
		if err == sql.ErrNoRows {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"message": "No active listing for origin",
			})
		}
		r.logger.Error("failed to get listing by origin", "error", err, "origin", c.Params("origin"))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to get listing: " + err.Error(),
		})
	}

	return c.JSON(result)
}

// GetListingsByOrigins retrieves active listings for multiple origins.
// @Summary Bulk lookup active listings by origin
// @Tags market
// @Accept json
// @Produce json
// @Param origins body []string true "Array of origins (txid_vout or txid.vout)"
// @Success 200 {object} object "Map of origin to listing"
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /market/origins [post]
func (r *Routes) GetListingsByOrigins(c *fiber.Ctx) error {
	var originStrs []string
	if err := c.BodyParser(&originStrs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid request body: expected array of origin strings",
		})
	}

	origins := make([]*transaction.Outpoint, 0, len(originStrs))
	for _, s := range originStrs {
		op, err := transaction.OutpointFromString(s)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid origin: " + s,
			})
		}
		origins = append(origins, op)
	}

	results, err := r.lookup.GetListingsByOrigins(c.Context(), r.topic, origins)
	if err != nil {
		r.logger.Error("failed to bulk lookup listings", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to lookup listings: " + err.Error(),
		})
	}

	return c.JSON(results)
}

// GetListing retrieves a single OrdLock listing by outpoint.
// @Summary Get listing
// @Tags market
// @Produce json
// @Param outpoint path string true "Outpoint (txid.vout)"
// @Success 200 {object} object
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /market/listing/{outpoint} [get]
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
