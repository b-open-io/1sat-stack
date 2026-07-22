package opns

import (
	"context"
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// CrawlTrigger starts the OpNS genesis crawl. It returns an error if the crawl
// is already running or cannot be started. The composition layer supplies the
// implementation (it owns the crawl's beef/engine dependencies).
type CrawlTrigger func(ctx context.Context) error

// AdminRoutes exposes opns's admin-guarded controls. The composition layer
// mounts these at the same public paths the admin UI already calls.
type AdminRoutes struct {
	trigger CrawlTrigger
	logger  *slog.Logger
}

// NewAdminRoutes creates opns admin routes. trigger may be nil when the crawl
// dependencies are unavailable; the endpoint then reports it is not available.
func NewAdminRoutes(trigger CrawlTrigger, logger *slog.Logger) *AdminRoutes {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminRoutes{trigger: trigger, logger: logger}
}

// Register mounts the opns admin routes on a Fiber group.
func (r *AdminRoutes) Register(group fiber.Router) {
	group.Post("/opns/crawl", r.handleTriggerCrawl)
	r.logger.Debug("registered opns admin routes")
}

// handleTriggerCrawl triggers the OpNS genesis crawl.
// @Summary Trigger OpNS crawl
// @Description Starts the OpNS genesis crawl
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "OpNS crawl not available"
// @Security BearerAuth
// @Router /api/opns/crawl [post]
func (r *AdminRoutes) handleTriggerCrawl(c *fiber.Ctx) error {
	if r.trigger == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "OpNS crawl not available — ensure opns and beef services are enabled",
		})
	}

	if err := r.trigger(c.Context()); err != nil {
		r.logger.Error("failed to trigger OpNS crawl", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"message": "OpNS crawl started"})
}
