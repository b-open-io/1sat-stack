package collection

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for the collection API.
type Routes struct {
	lookup *LookupService
	logger *slog.Logger
}

// NewRoutes creates collection HTTP routes.
func NewRoutes(lookup *LookupService, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{lookup: lookup, logger: logger.With("component", "collection-routes")}
}

// Register mounts collection routes on the router.
func (r *Routes) Register(router fiber.Router) {
	router.Get("/", r.ListCollections)
	router.Get("/:collectionId", r.GetCollection)
	router.Get("/:collectionId/items", r.ListItems)
	router.Get("/:collectionId/item/:outpoint", r.GetItem)
}

// ListCollections returns discovered collections.
// @Summary List collections
// @Tags collection
// @Produce json
// @Param limit query int false "Max results" default(100)
// @Param rev query bool false "Reverse score order"
// @Success 200 {array} Entry
// @Router / [get]
func (r *Routes) ListCollections(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 100)
	reverse := c.QueryBool("rev", false)
	entries, err := r.lookup.ListCollections(c.Context(), limit, reverse)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entries == nil {
		entries = []*Entry{}
	}
	return c.JSON(entries)
}

// GetCollection returns one collection by collectionId (outpoint).
// @Summary Get collection
// @Tags collection
// @Produce json
// @Param collectionId path string true "Collection outpoint (txid_vout)"
// @Success 200 {object} Entry
// @Failure 404 {object} object{error=string}
// @Router /{collectionId} [get]
func (r *Routes) GetCollection(c *fiber.Ctx) error {
	collectionID := c.Params("collectionId")
	op, err := parseOutpoint(collectionID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid collectionId"})
	}
	collectionID = op.OrdinalString()

	entry, err := r.lookup.GetCollection(c.Context(), collectionID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entry == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "collection not found"})
	}
	return c.JSON(entry)
}

// ListItems returns items for a collection.
// @Summary List collection items
// @Tags collection
// @Produce json
// @Param collectionId path string true "Collection outpoint"
// @Param limit query int false "Max results" default(100)
// @Param rev query bool false "Reverse score order"
// @Success 200 {array} Entry
// @Router /{collectionId}/items [get]
func (r *Routes) ListItems(c *fiber.Ctx) error {
	collectionID := c.Params("collectionId")
	op, err := parseOutpoint(collectionID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid collectionId"})
	}
	collectionID = op.OrdinalString()
	limit := c.QueryInt("limit", 100)
	reverse := c.QueryBool("rev", false)

	entries, err := r.lookup.ListItems(c.Context(), collectionID, limit, reverse)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entries == nil {
		entries = []*Entry{}
	}
	return c.JSON(entries)
}

// GetItem returns a single collection item.
// @Summary Get collection item
// @Tags collection
// @Produce json
// @Param collectionId path string true "Collection outpoint"
// @Param outpoint path string true "Item outpoint"
// @Success 200 {object} Entry
// @Failure 404 {object} object{error=string}
// @Router /{collectionId}/item/{outpoint} [get]
func (r *Routes) GetItem(c *fiber.Ctx) error {
	collectionID := c.Params("collectionId")
	op, err := parseOutpoint(collectionID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid collectionId"})
	}
	collectionID = op.OrdinalString()
	outpoint := c.Params("outpoint")

	entry, err := r.lookup.GetItem(c.Context(), collectionID, outpoint)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entry == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "item not found"})
	}
	return c.JSON(entry)
}
