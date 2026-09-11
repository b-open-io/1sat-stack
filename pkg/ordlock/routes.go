package ordlock

import (
	"database/sql"
	"log/slog"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

type Routes struct {
	ordlock *OrdLock
	logger  *slog.Logger
}

func NewRoutes(ordlock *OrdLock, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		ordlock: ordlock,
		logger:  logger,
	}
}

func (r *Routes) Register(router fiber.Router) {
	router.Get("/listings", r.SearchListings)
	router.Get("/listings/owner/:address", r.GetListingsByOwner)
	router.Get("/listing/:outpoint", r.GetListing)
	router.Get("/origin/:origin", r.GetListingByOrigin)
	router.Post("/origins", r.GetListingsByOrigins)
}

func (r *Routes) logDeprecatedPublicQuery(endpoint string) {
	r.logger.Info("deprecated listing index queried", "endpoint", endpoint)
}

// SearchListings is the public market browse/search index.
// Deprecated OrdLock listings are omitted from public discovery.
// @Summary Search listings
// @Description Public market index. Deprecated listings are omitted; use outpoint or owner lookup for remaining inventory.
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
// @Router /listings [get]
func (r *Routes) SearchListings(c *fiber.Ctx) error {
	r.logDeprecatedPublicQuery("listings")
	// Non-nil so a no-match search marshals to [] rather than null.
	return c.JSON(make([]any, 0))
}

// GetListingByOrigin is a public discovery lookup by origin.
// Deprecated OrdLock listings are omitted from public discovery.
// @Summary Get active listing by origin
// @Description Public origin index. Deprecated listings are omitted; use outpoint or owner lookup for remaining inventory.
// @Tags market
// @Produce json
// @Param origin path string true "Origin (txid_vout or txid.vout)"
// @Success 200 {object} object
// @Failure 400 {object} object{message=string}
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /origin/{origin} [get]
func (r *Routes) GetListingByOrigin(c *fiber.Ctx) error {
	if _, err := transaction.OutpointFromString(c.Params("origin")); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid origin format",
		})
	}
	r.logDeprecatedPublicQuery("origin")
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
		"message": "No active listing for origin",
	})
}

// GetListingsByOrigins is a public bulk discovery lookup by origin.
// Deprecated OrdLock listings are omitted from public discovery.
// @Summary Bulk lookup active listings by origin
// @Description Public origin index. Deprecated listings are omitted; use outpoint or owner lookup for remaining inventory.
// @Tags market
// @Accept json
// @Produce json
// @Param origins body []string true "Array of origins (txid_vout or txid.vout)"
// @Success 200 {object} object "Map of origin to listing"
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /origins [post]
func (r *Routes) GetListingsByOrigins(c *fiber.Ctx) error {
	var originStrs []string
	if err := c.BodyParser(&originStrs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid request body: expected array of origin strings",
		})
	}

	for _, s := range originStrs {
		if _, err := transaction.OutpointFromString(s); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid origin: " + s,
			})
		}
	}

	r.logDeprecatedPublicQuery("origins")
	return c.JSON(map[string]any{})
}

// GetListingsByOwner returns listings for a seller address so wallets can
// cancel, purchase, or migrate remaining inventory.
// @Summary Get listings by owner address
// @Description Owner lookup for remaining listings. Used by wallets to cancel or migrate.
// @Tags market
// @Produce json
// @Param address path string true "Seller address"
// @Param status query string false "Listing status: active, sale, cancel" default(active)
// @Param limit query int false "Results limit" default(20)
// @Success 200 {array} object
// @Failure 400 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /listings/owner/{address} [get]
func (r *Routes) GetListingsByOwner(c *fiber.Ctx) error {
	address := c.Params("address")
	if address == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Address is required",
		})
	}
	status := c.Query("status", "active")
	limit := c.QueryInt("limit", 20)

	results, err := r.ordlock.GetListingsBySeller(c.Context(), address, status, limit)
	if err != nil {
		r.logger.Error("failed to get listings by owner", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to get listings: " + err.Error(),
		})
	}

	return c.JSON(results)
}

// GetListing retrieves a single listing by outpoint for wallet cancel,
// purchase, or migrate. Public browse does not use this path.
// @Summary Get listing
// @Description Outpoint lookup for remaining listings. Used by wallets to cancel, purchase, or migrate.
// @Tags market
// @Produce json
// @Param outpoint path string true "Outpoint (txid.vout)"
// @Success 200 {object} object
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /listing/{outpoint} [get]
func (r *Routes) GetListing(c *fiber.Ctx) error {
	op, err := transaction.OutpointFromString(c.Params("outpoint"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid outpoint format",
		})
	}

	result, err := r.ordlock.GetListing(c.Context(), op.Bytes())
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
