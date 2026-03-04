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
	router.Get("/origin/:name", r.GetOrigin)
	router.Get("/mine/:name", r.GetMine)
}

// GetOrigin returns the current outpoint for a registered OpNS domain.
// @Summary Get domain origin outpoint
// @Tags opns
// @Produce json
// @Param name path string true "Domain name"
// @Success 200 {object} object{name=string,outpoint=string}
// @Failure 400 {object} object{error=string}
// @Failure 404 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Router /opns/origin/{name} [get]
func (r *Routes) GetOrigin(c *fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Missing name",
		})
	}

	outpoint, err := r.lookup.Origin(c.Context(), name)
	if err != nil {
		r.logger.Error("failed to look up domain origin",
			"domain", name,
			"error", err,
		)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if outpoint == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Name not registered",
		})
	}

	return c.JSON(fiber.Map{
		"name":     name,
		"outpoint": outpoint.String(),
	})
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
