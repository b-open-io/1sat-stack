package opns

import (
	"log/slog"

	"github.com/bsv-blockchain/go-sdk/transaction"
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
	router.Post("/origins", r.ValidateOrigins)
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
// @Router /origin/{name} [get]
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
// @Router /mine/{name} [get]
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

// ValidateOrigins checks which outpoints exist as registered OpNS names.
// @Summary Bulk validate OpNS origins
// @Tags opns
// @Accept json
// @Produce json
// @Param outpoints body []string true "Array of outpoints (txid_vout or txid.vout)"
// @Success 200 {object} object "Map of outpoint string to boolean"
// @Failure 400 {object} object{error=string}
// @Failure 500 {object} object{error=string}
// @Router /origins [post]
func (r *Routes) ValidateOrigins(c *fiber.Ctx) error {
	var strs []string
	if err := c.BodyParser(&strs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body: expected array of outpoint strings",
		})
	}

	outpoints := make([]*transaction.Outpoint, 0, len(strs))
	for _, s := range strs {
		op, err := transaction.OutpointFromString(s)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid outpoint: " + s,
			})
		}
		outpoints = append(outpoints, op)
	}

	valid, err := r.lookup.ValidateOrigins(c.Context(), outpoints)
	if err != nil {
		r.logger.Error("failed to validate origins", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(valid)
}
