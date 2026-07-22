package overlay

import (
	"log/slog"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/gofiber/fiber/v2"
)

// AdminRoutes exposes overlay-engine admin controls: the active topic/lookup
// listings (across every running module engine) and per-topic remote-sync
// configuration. The composition layer mounts these at the same public paths
// the admin UI already calls.
type AdminRoutes struct {
	engines  map[string]*engine.Engine
	services *Services
	logger   *slog.Logger
}

// NewAdminRoutes creates overlay admin routes. engines maps module name to its
// engine (for topic/lookup listing); services provides remote-config storage.
func NewAdminRoutes(engines map[string]*engine.Engine, services *Services, logger *slog.Logger) *AdminRoutes {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminRoutes{engines: engines, services: services, logger: logger}
}

// Register mounts the overlay admin routes on a Fiber group.
func (r *AdminRoutes) Register(group fiber.Router) {
	group.Get("/topics/active", r.handleGetActiveTopics)
	group.Get("/topics/:name/remotes", r.handleGetTopicRemotes)
	group.Put("/topics/:name/remotes", r.handleSetTopicRemotes)
	group.Delete("/topics/:name/remotes", r.handleDeleteTopicRemotes)
	group.Get("/lookups/active", r.handleGetActiveLookups)
	r.logger.Debug("registered overlay admin routes")
}

// handleGetActiveTopics returns the list of currently active topics.
// @Summary Get active topics
// @Description Returns the list of currently active topics from the overlay engine
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of active topics"
// @Security BearerAuth
// @Router /api/topics/active [get]
func (r *AdminRoutes) handleGetActiveTopics(c *fiber.Ctx) error {
	var topics []string
	for _, eng := range r.engines {
		for name := range eng.ListTopicManagers() {
			topics = append(topics, name)
		}
	}
	if topics == nil {
		topics = []string{}
	}
	return c.JSON(topics)
}

// handleGetActiveLookups returns the list of currently active lookup services.
// @Summary Get active lookup services
// @Description Returns the list of currently active lookup services from the overlay engine
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of active lookup services"
// @Security BearerAuth
// @Router /api/lookups/active [get]
func (r *AdminRoutes) handleGetActiveLookups(c *fiber.Ctx) error {
	var lookups []string
	for _, eng := range r.engines {
		for name := range eng.ListLookupServiceProviders() {
			lookups = append(lookups, name)
		}
	}
	if lookups == nil {
		lookups = []string{}
	}
	return c.JSON(lookups)
}

// handleGetTopicRemotes returns the remote configuration for a topic.
// @Summary Get topic remotes
// @Description Returns the configured remotes for a topic
// @Tags admin
// @Produce json
// @Param name path string true "Topic name"
// @Success 200 {array} RemoteConfig "List of configured remotes"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/topics/{name}/remotes [get]
func (r *AdminRoutes) handleGetTopicRemotes(c *fiber.Ctx) error {
	if r.services == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "overlay service not available"})
	}

	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "topic name is required"})
	}

	configs, err := r.services.GetRemoteConfig(c.Context(), name)
	if err != nil {
		r.logger.Error("failed to get topic remotes", "error", err, "topic", name)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get topic remotes"})
	}

	if configs == nil {
		configs = []RemoteConfig{}
	}

	return c.JSON(configs)
}

// handleSetTopicRemotes sets the remote configuration for a topic.
// @Summary Set topic remotes
// @Description Sets the configured remotes for a topic (overrides defaults)
// @Tags admin
// @Accept json
// @Produce json
// @Param name path string true "Topic name"
// @Param body body []RemoteConfig true "Remote configurations"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/topics/{name}/remotes [put]
func (r *AdminRoutes) handleSetTopicRemotes(c *fiber.Ctx) error {
	if r.services == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "overlay service not available"})
	}

	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "topic name is required"})
	}

	var configs []RemoteConfig
	if err := c.BodyParser(&configs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if err := r.services.SaveRemoteConfig(c.Context(), name, configs); err != nil {
		r.logger.Error("failed to save topic remotes", "error", err, "topic", name)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to save topic remotes"})
	}

	r.logger.Info("saved topic remotes", "topic", name, "remotes", len(configs))
	return c.JSON(fiber.Map{"message": "topic remotes saved", "topic": name, "count": len(configs)})
}

// handleDeleteTopicRemotes deletes the remote configuration for a topic.
// @Summary Delete topic remotes
// @Description Removes the remote config override, reverting to defaults
// @Tags admin
// @Produce json
// @Param name path string true "Topic name"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/topics/{name}/remotes [delete]
func (r *AdminRoutes) handleDeleteTopicRemotes(c *fiber.Ctx) error {
	if r.services == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "overlay service not available"})
	}

	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "topic name is required"})
	}

	if err := r.services.DeleteRemoteConfig(c.Context(), name); err != nil {
		r.logger.Error("failed to delete topic remotes", "error", err, "topic", name)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete topic remotes"})
	}

	r.logger.Info("deleted topic remotes", "topic", name)
	return c.JSON(fiber.Map{"message": "topic remotes deleted (reverted to defaults)", "topic": name})
}
