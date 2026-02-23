package bsocial

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP handlers for BSocial API.
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

// Register registers the BSocial routes with the Fiber router.
func (r *Routes) Register(router fiber.Router) {
	router.Get("/post/search", r.SearchPosts)
}

// SearchPosts searches for BSocial posts using full-text search.
// @Summary Search posts
// @Tags bsocial
// @Produce json
// @Param q query string true "Search query"
// @Param limit query int false "Results limit" default(20)
// @Param offset query int false "Results offset" default(0)
// @Success 200 {array} object
// @Failure 500 {object} object{message=string}
// @Router /bsocial/post/search [get]
func (r *Routes) SearchPosts(c *fiber.Ctx) error {
	q := c.Query("q")
	limit := c.QueryInt("limit", 20)
	offset := c.QueryInt("offset", 0)

	results, err := r.lookup.Search(c.Context(), q, limit, offset)
	if err != nil {
		r.logger.Error("failed to search posts", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to search posts: " + err.Error(),
		})
	}

	return c.JSON(results)
}
