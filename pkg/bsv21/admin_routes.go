package bsv21

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/gofiber/fiber/v2"
)

// AdminRoutes exposes bsv21's admin-guarded controls: the token whitelist and
// blacklist (stored in the config store) and the active token-worker status.
// The composition layer mounts these at the same public paths the admin UI
// already calls.
type AdminRoutes struct {
	configStore config.Store
	sync        *SyncServices
	logger      *slog.Logger
}

// NewAdminRoutes creates bsv21 admin routes. sync may be nil when the sync
// pipeline is not running; the worker endpoint then returns an empty list.
func NewAdminRoutes(cs config.Store, sync *SyncServices, logger *slog.Logger) *AdminRoutes {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminRoutes{configStore: cs, sync: sync, logger: logger}
}

// Register mounts the bsv21 admin routes on a Fiber group.
func (r *AdminRoutes) Register(group fiber.Router) {
	group.Get("/whitelist", r.handleGetWhitelist)
	group.Post("/whitelist", r.handleAddToWhitelist)
	group.Delete("/whitelist/:token", r.handleRemoveFromWhitelist)

	group.Get("/blacklist", r.handleGetBlacklist)
	group.Post("/blacklist", r.handleAddToBlacklist)
	group.Delete("/blacklist/:topic", r.handleRemoveFromBlacklist)

	group.Get("/bsv21/workers", r.handleGetWorkers)

	r.logger.Debug("registered bsv21 admin routes")
}

// handleGetWhitelist returns the list of whitelisted BSV21 tokens.
// @Summary Get whitelist
// @Description Returns the list of whitelisted BSV21 tokens (always active)
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of whitelisted tokens"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/whitelist [get]
func (r *AdminRoutes) handleGetWhitelist(c *fiber.Ctx) error {
	entries, err := r.configStore.List(c.Context(), "bsv21.whitelist:")
	if err != nil {
		r.logger.Error("failed to get whitelist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get whitelist"})
	}
	tokens := make([]string, 0, len(entries))
	for key := range entries {
		tokens = append(tokens, strings.TrimPrefix(key, "bsv21.whitelist:"))
	}
	sort.Strings(tokens)
	return c.JSON(tokens)
}

// handleAddToWhitelist adds a token to the whitelist.
// @Summary Add to whitelist
// @Description Adds a BSV21 token to the whitelist (always active)
// @Tags admin
// @Accept json
// @Produce json
// @Param body body object{topic=string} true "Token to add"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/whitelist [post]
func (r *AdminRoutes) handleAddToWhitelist(c *fiber.Ctx) error {
	var req struct {
		Topic string `json:"topic"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "token is required"})
	}

	if err := r.configStore.Set(c.Context(), "bsv21.whitelist:"+req.Topic, "1"); err != nil {
		r.logger.Error("failed to add to whitelist", "error", err, "token", req.Topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to add to whitelist"})
	}

	r.logger.Info("added token to whitelist", "token", req.Topic)
	return c.JSON(fiber.Map{"message": "token added to whitelist", "token": req.Topic})
}

// handleRemoveFromWhitelist removes a token from the whitelist.
// @Summary Remove from whitelist
// @Description Removes a BSV21 token from the whitelist
// @Tags admin
// @Produce json
// @Param token path string true "Token ID to remove"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/whitelist/{token} [delete]
func (r *AdminRoutes) handleRemoveFromWhitelist(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "token is required"})
	}

	if err := r.configStore.Delete(c.Context(), "bsv21.whitelist:"+token); err != nil {
		r.logger.Error("failed to remove from whitelist", "error", err, "token", token)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to remove from whitelist"})
	}

	r.logger.Info("removed token from whitelist", "token", token)
	return c.JSON(fiber.Map{"message": "token removed from whitelist", "token": token})
}

// handleGetBlacklist returns the list of blacklisted topics.
// @Summary Get blacklist
// @Description Returns the list of blacklisted topics
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of blacklisted topics"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/blacklist [get]
func (r *AdminRoutes) handleGetBlacklist(c *fiber.Ctx) error {
	entries, err := r.configStore.List(c.Context(), "bsv21.blacklist:")
	if err != nil {
		r.logger.Error("failed to get blacklist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get blacklist"})
	}
	topics := make([]string, 0, len(entries))
	for key := range entries {
		topics = append(topics, strings.TrimPrefix(key, "bsv21.blacklist:"))
	}
	sort.Strings(topics)
	return c.JSON(topics)
}

// handleAddToBlacklist adds a topic to the blacklist.
// @Summary Add to blacklist
// @Description Adds a topic to the blacklist
// @Tags admin
// @Accept json
// @Produce json
// @Param body body object{topic=string} true "Topic to add"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/blacklist [post]
func (r *AdminRoutes) handleAddToBlacklist(c *fiber.Ctx) error {
	var req struct {
		Topic string `json:"topic"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "topic is required"})
	}

	if err := r.configStore.Set(c.Context(), "bsv21.blacklist:"+req.Topic, "1"); err != nil {
		r.logger.Error("failed to add to blacklist", "error", err, "topic", req.Topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to add to blacklist"})
	}

	r.logger.Info("added topic to blacklist", "topic", req.Topic)
	return c.JSON(fiber.Map{"message": "topic added to blacklist", "topic": req.Topic})
}

// handleRemoveFromBlacklist removes a topic from the blacklist.
// @Summary Remove from blacklist
// @Description Removes a topic from the blacklist
// @Tags admin
// @Produce json
// @Param topic path string true "Topic ID to remove"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/blacklist/{topic} [delete]
func (r *AdminRoutes) handleRemoveFromBlacklist(c *fiber.Ctx) error {
	topic := c.Params("topic")
	if topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "topic is required"})
	}

	if err := r.configStore.Delete(c.Context(), "bsv21.blacklist:"+topic); err != nil {
		r.logger.Error("failed to remove from blacklist", "error", err, "topic", topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to remove from blacklist"})
	}

	r.logger.Info("removed topic from blacklist", "topic", topic)
	return c.JSON(fiber.Map{"message": "topic removed from blacklist", "topic": topic})
}

// handleGetWorkers returns the status of all active BSV21 token workers.
// @Summary Get BSV21 workers
// @Description Returns the status of all active BSV21 token workers
// @Tags admin
// @Produce json
// @Success 200 {array} WorkerStatus "List of active workers"
// @Security BearerAuth
// @Router /api/bsv21/workers [get]
func (r *AdminRoutes) handleGetWorkers(c *fiber.Ctx) error {
	if r.sync == nil {
		r.logger.Debug("bsv21 workers: sync service is nil")
		return c.JSON([]WorkerStatus{})
	}

	manager := r.sync.GetManager()
	if manager == nil {
		r.logger.Debug("bsv21 workers: manager is nil")
		return c.JSON([]WorkerStatus{})
	}

	workers := manager.ListWorkers(c.Context())
	if workers == nil {
		workers = []WorkerStatus{}
	}

	sort.Slice(workers, func(i, j int) bool {
		return workers[i].TokenID < workers[j].TokenID
	})

	return c.JSON(workers)
}
