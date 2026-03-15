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
	router.Post("/identity/validByAddress", r.ValidByAddress)
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

	currentAddr, validChain := ResolveCurrentAddress(id)
	id.CurrentAddress = currentAddr
	id.Addresses = validChain

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

	for i := range identities {
		currentAddr, validChain := ResolveCurrentAddress(&identities[i])
		identities[i].CurrentAddress = currentAddr
		identities[i].Addresses = validChain
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

	for i := range profiles {
		currentAddr, validChain := ResolveCurrentAddress(&profiles[i])
		profiles[i].CurrentAddress = currentAddr
		profiles[i].Addresses = validChain
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

	currentAddr, validChain := ResolveCurrentAddress(identity)
	identity.CurrentAddress = currentAddr
	identity.Addresses = validChain

	return c.JSON(identity.Profile)
}

// ValidByAddress checks if an address is currently valid for a BAP identity.
// @Summary Check address validity for identity
// @Tags bap
// @Accept json
// @Produce json
// @Param request body ValidByAddressRequest true "Address and optional block height"
// @Success 200 {object} ValidByAddressResponse
// @Failure 400 {object} object{message=string}
// @Failure 404 {object} object{message=string}
// @Failure 500 {object} object{message=string}
// @Router /bap/identity/validByAddress [post]
func (r *Routes) ValidByAddress(c *fiber.Ctx) error {
	var req ValidByAddressRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid request body: " + err.Error(),
		})
	}

	if req.Address == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Address is required",
		})
	}

	identity, err := r.lookup.LoadIdentityByAddress(c.Context(), req.Address)
	if err != nil {
		r.logger.Error("failed to fetch identity by address", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to fetch identity: " + err.Error(),
		})
	}

	if identity == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"message": "Identity not found for address: " + req.Address,
		})
	}

	currentAddr, validChain := ResolveCurrentAddress(identity)
	identity.CurrentAddress = currentAddr
	identity.Addresses = validChain

	var valid bool
	var validBlock uint32

	if req.Block > 0 {
		for _, addr := range validChain {
			if addr.Block <= req.Block {
				valid = addr.Address == req.Address
				validBlock = addr.Block
			}
		}
	} else {
		valid = req.Address == currentAddr
	}

	resp := ValidByAddressResponse{
		Identity:       *identity,
		ValidityRecord: ValidityRecord{Valid: valid, Block: validBlock},
	}
	if valid {
		resp.Profile = identity.Profile
	}

	return c.JSON(resp)
}
