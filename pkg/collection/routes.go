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
	router.Get("/", r.ListRoots)
	router.Get("/:collectionId", r.GetRoot)
	router.Get("/:collectionId/members", r.ListMembers)
	router.Get("/:collectionId/member/:outpoint", r.GetMember)
}

// ListRoots returns discovered collection roots.
// @Summary List collection roots
// @Tags collection
// @Produce json
// @Param limit query int false "Max results" default(100)
// @Param rev query bool false "Reverse score order"
// @Success 200 {array} Entry
// @Router / [get]
func (r *Routes) ListRoots(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 100)
	reverse := c.QueryBool("rev", false)
	entries, err := r.lookup.ListRoots(c.Context(), limit, reverse)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entries == nil {
		entries = []*Entry{}
	}
	return c.JSON(entries)
}

// GetRoot returns one collection root by collectionId (root outpoint).
// @Summary Get collection root
// @Tags collection
// @Produce json
// @Param collectionId path string true "Collection root outpoint (txid_vout)"
// @Success 200 {object} Entry
// @Failure 404 {object} object{error=string}
// @Router /{collectionId} [get]
func (r *Routes) GetRoot(c *fiber.Ctx) error {
	collectionID := c.Params("collectionId")
	if _, err := parseOutpoint(collectionID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid collectionId"})
	}
	// normalize to ordinal form for storage key
	op, _ := parseOutpoint(collectionID)
	collectionID = op.OrdinalString()

	entry, err := r.lookup.GetRoot(c.Context(), collectionID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entry == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "collection not found"})
	}
	return c.JSON(entry)
}

// ListMembers returns members for a collection.
// @Summary List collection members
// @Tags collection
// @Produce json
// @Param collectionId path string true "Collection root outpoint"
// @Param limit query int false "Max results" default(100)
// @Param rev query bool false "Reverse score order"
// @Success 200 {array} Entry
// @Router /{collectionId}/members [get]
func (r *Routes) ListMembers(c *fiber.Ctx) error {
	collectionID := c.Params("collectionId")
	op, err := parseOutpoint(collectionID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid collectionId"})
	}
	collectionID = op.OrdinalString()
	limit := c.QueryInt("limit", 100)
	reverse := c.QueryBool("rev", false)

	entries, err := r.lookup.ListMembers(c.Context(), collectionID, limit, reverse)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entries == nil {
		entries = []*Entry{}
	}
	return c.JSON(entries)
}

// GetMember returns a single member.
// @Summary Get collection member
// @Tags collection
// @Produce json
// @Param collectionId path string true "Collection root outpoint"
// @Param outpoint path string true "Member outpoint"
// @Success 200 {object} Entry
// @Failure 404 {object} object{error=string}
// @Router /{collectionId}/member/{outpoint} [get]
func (r *Routes) GetMember(c *fiber.Ctx) error {
	collectionID := c.Params("collectionId")
	op, err := parseOutpoint(collectionID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid collectionId"})
	}
	collectionID = op.OrdinalString()
	outpoint := c.Params("outpoint")

	entry, err := r.lookup.GetMember(c.Context(), collectionID, outpoint)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if entry == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "member not found"})
	}
	return c.JSON(entry)
}
