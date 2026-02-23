package bap

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for BAP API.
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

// Register registers the BAP routes with the Fiber router.
func (r *Routes) Register(router fiber.Router) {
	router.Post("/identity/get", r.GetIdentity)
	router.Get("/identity/search", r.SearchIdentities)
	router.Get("/profile", r.ListProfiles)
	router.Get("/profile/:bapId", r.GetProfileByBapId)
}

// GetIdentity retrieves a BAP identity by its ID key.
// @Summary Get identity by ID
// @Tags bap
// @Accept json
// @Produce json
// @Param request body object{idKey=string} true "Identity request with idKey field"
// @Success 200 {object} Identity
// @Failure 400 {object} object{message=string}
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /bap/identity/get [post]
func (r *Routes) GetIdentity(c *fiber.Ctx) error {
	req := map[string]string{}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid request body: " + err.Error(),
		})
	}

	id, err := r.lookup.LoadIdentityById(c.Context(), req["idKey"])
	if err != nil {
		r.logger.Error("failed to fetch identity", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to fetch identity: " + err.Error(),
		})
	}

	if id == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"message": "Identity not found for ID key: " + req["idKey"],
		})
	}

	return c.JSON(id)
}

// SearchIdentities searches for BAP identities using full-text search.
// @Summary Search identities
// @Tags bap
// @Produce json
// @Param q query string true "Search query"
// @Param limit query int false "Results limit" default(20)
// @Param offset query int false "Results offset" default(0)
// @Success 200 {array} Identity
// @Failure 500 {object} object{message=string}
// @Router /bap/identity/search [get]
func (r *Routes) SearchIdentities(c *fiber.Ctx) error {
	q := c.Query("q")
	limit := c.QueryInt("limit", 20)
	offset := c.QueryInt("offset", 0)

	identities, err := r.lookup.Search(c.Context(), q, limit, offset)
	if err != nil {
		r.logger.Error("failed to search identities", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to search identities: " + err.Error(),
		})
	}

	return c.JSON(identities)
}

// ListProfiles returns a paginated list of BAP profiles.
// @Summary List profiles
// @Tags bap
// @Produce json
// @Param limit query int false "Results limit" default(20)
// @Param offset query int false "Results offset" default(0)
// @Success 200 {array} Profile
// @Failure 500 {object} object{message=string}
// @Router /bap/profile [get]
func (r *Routes) ListProfiles(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 20)
	offset := c.QueryInt("offset", 0)

	profiles, err := r.lookup.LoadProfiles(c.Context(), limit, offset)
	if err != nil {
		r.logger.Error("failed to fetch profiles", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to fetch profiles: " + err.Error(),
		})
	}

	return c.JSON(profiles)
}

// GetProfileByBapId retrieves profile data for a specific BAP identity.
// @Summary Get profile by BAP ID
// @Tags bap
// @Produce json
// @Param bapId path string true "BAP Identity ID"
// @Success 200 {object} object
// @Failure 500 {object} object{message=string}
// @Router /bap/profile/{bapId} [get]
func (r *Routes) GetProfileByBapId(c *fiber.Ctx) error {
	bapId := c.Params("bapId")

	identity, err := r.lookup.LoadIdentityById(c.Context(), bapId)
	if err != nil {
		r.logger.Error("failed to fetch profile", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to fetch profile: " + err.Error(),
		})
	}

	if identity == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"message": "Profile not found for BAP ID: " + bapId,
		})
	}

	return c.JSON(identity.Profile)
}
