package opns

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for OpNS API.
type Routes struct {
	lookup *LookupService
	logger *slog.Logger
}

// NewRoutes creates a new Routes instance.
func NewRoutes(lookup *LookupService, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		lookup: lookup,
		logger: logger,
	}
}

// Register registers the OpNS routes with the Fiber router.
func (r *Routes) Register(router fiber.Router) {
	router.Get("/owner/:name", r.GetOwner)
	router.Get("/mine/:name", r.GetMine)
}

// GetOwner retrieves the owner of an OpNS domain.
// @Summary Get domain owner
// @Tags opns
// @Produce json
// @Param name path string true "Domain name"
// @Success 200 {object} OwnerResult
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Router /opns/owner/{name} [get]
func (r *Routes) GetOwner(c *fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Missing name",
		})
	}

	owner, err := r.lookup.Owner(c.Context(), name)
	if err != nil {
		r.logger.Error("failed to look up domain owner",
			"domain", name,
			"error", err,
		)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if owner == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "No owner found",
		})
	}

	return c.JSON(owner)
}

// GetMine retrieves the mining status of an OpNS domain.
// @Summary Get domain mining status
// @Tags opns
// @Produce json
// @Param name path string true "Domain name"
// @Success 200 {object} MineResult
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Router /opns/mine/{name} [get]
func (r *Routes) GetMine(c *fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Missing name",
		})
	}

	result, err := r.lookup.Mine(c.Context(), name)
	if err != nil {
		r.logger.Error("failed to look up domain mining status",
			"domain", name,
			"error", err,
		)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if result == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "No outpoint found",
		})
	}

	return c.JSON(result)
}
