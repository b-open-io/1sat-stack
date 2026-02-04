package admin

import (
	"embed"
	"encoding/binary"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed ui/*
var uiFS embed.FS

// Routes handles admin HTTP routes
type Routes struct {
	overlay   *overlay.Services
	store     store.Store
	bsv21Sync *bsv21.SyncServices
	config    *RoutesConfig
	logger    *slog.Logger
}

// NewRoutes creates a new Routes instance
func NewRoutes(overlaySvc *overlay.Services, s store.Store, bsv21Sync *bsv21.SyncServices, cfg *RoutesConfig, logger *slog.Logger) *Routes {
	return &Routes{
		overlay:   overlaySvc,
		store:     s,
		bsv21Sync: bsv21Sync,
		config:    cfg,
		logger:    logger,
	}
}

// Register registers admin routes on a Fiber app group
func (r *Routes) Register(group fiber.Router) {
	// Apply auth middleware if bearer token is configured
	if r.config.BearerToken != "" {
		group.Use(r.authMiddleware())
		r.logger.Info("admin bearer token authentication enabled")
	}

	// Whitelist endpoints (tokens always active)
	group.Get("/whitelist", r.handleGetWhitelist)
	group.Post("/whitelist", r.handleAddToWhitelist)
	group.Delete("/whitelist/:token", r.handleRemoveFromWhitelist)

	// Blacklist endpoints (tokens never active)
	group.Get("/blacklist", r.handleGetBlacklist)
	group.Post("/blacklist", r.handleAddToBlacklist)
	group.Delete("/blacklist/:token", r.handleRemoveFromBlacklist)

	// Active topics endpoint
	group.Get("/topics/active", r.handleGetActiveTopics)

	// Topic remote configuration endpoints
	group.Get("/topics/:name/remotes", r.handleGetTopicRemotes)
	group.Put("/topics/:name/remotes", r.handleSetTopicRemotes)
	group.Delete("/topics/:name/remotes", r.handleDeleteTopicRemotes)

	// Active lookup services endpoint
	group.Get("/lookups/active", r.handleGetActiveLookups)

	// Progress endpoints
	group.Get("/progress", r.handleGetProgress)
	group.Put("/progress/:id", r.handleUpdateProgress)
	group.Delete("/progress/:id", r.handleDeleteProgress)

	// BSV21 worker endpoints
	group.Get("/bsv21/workers", r.handleGetBSV21Workers)

	// Data query routes
	dataRoutes := NewDataRoutes(r.store, r.logger)
	dataRoutes.Register(group.Group("/data"))

	// Serve static UI files
	uiSubFS, err := fs.Sub(uiFS, "ui")
	if err != nil {
		r.logger.Error("failed to create ui sub filesystem", "error", err)
		return
	}

	// Serve index.html for root and any non-API routes
	group.Get("/", func(c *fiber.Ctx) error {
		content, err := fs.ReadFile(uiSubFS, "index.html")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("Not found")
		}
		c.Set("Content-Type", "text/html")
		return c.Send(content)
	})

	// Serve other static files
	group.Use("/", filesystem.New(filesystem.Config{
		Root:   http.FS(uiSubFS),
		Browse: false,
	}))

	r.logger.Debug("registered admin routes")
}

// authMiddleware validates bearer token authentication
func (r *Routes) authMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		expected := "Bearer " + r.config.BearerToken
		if auth != expected {
			r.logger.Warn("unauthorized admin access attempt",
				"path", c.Path(),
				"method", c.Method(),
				"ip", c.IP())
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "unauthorized",
			})
		}
		return c.Next()
	}
}

// handleGetWhitelist returns the list of whitelisted BSV21 tokens
// @Summary Get whitelist
// @Description Returns the list of whitelisted BSV21 tokens (always active)
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of whitelisted tokens"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/whitelist [get]
func (r *Routes) handleGetWhitelist(c *fiber.Ctx) error {
	members, err := r.store.SMembers(c.Context(), bsv21.KeyWhitelist)
	if err != nil {
		r.logger.Error("failed to get whitelist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get whitelist",
		})
	}
	tokens := make([]string, len(members))
	for i, m := range members {
		tokens[i] = string(m)
	}
	return c.JSON(tokens)
}

// handleAddToWhitelist adds a token to the whitelist
// @Summary Add to whitelist
// @Description Adds a BSV21 token to the whitelist (always active)
// @Tags admin
// @Accept json
// @Produce json
// @Param body body object true "Token to add" example({"topic": "abc123..."})
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/whitelist [post]
func (r *Routes) handleAddToWhitelist(c *fiber.Ctx) error {
	var req struct {
		Topic string `json:"topic"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "token is required",
		})
	}

	if err := r.store.SAdd(c.Context(), bsv21.KeyWhitelist, []byte(req.Topic)); err != nil {
		r.logger.Error("failed to add to whitelist", "error", err, "token", req.Topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to add to whitelist",
		})
	}

	r.logger.Info("added token to whitelist", "token", req.Topic)
	return c.JSON(fiber.Map{
		"message": "token added to whitelist",
		"token":   req.Topic,
	})
}

// handleRemoveFromWhitelist removes a token from the whitelist
// @Summary Remove from whitelist
// @Description Removes a BSV21 token from the whitelist
// @Tags admin
// @Produce json
// @Param token path string true "Token ID to remove"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/whitelist/{token} [delete]
func (r *Routes) handleRemoveFromWhitelist(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "token is required",
		})
	}

	if err := r.store.SRem(c.Context(), bsv21.KeyWhitelist, []byte(token)); err != nil {
		r.logger.Error("failed to remove from whitelist", "error", err, "token", token)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to remove from whitelist",
		})
	}

	r.logger.Info("removed token from whitelist", "token", token)
	return c.JSON(fiber.Map{
		"message": "token removed from whitelist",
		"token":   token,
	})
}

// handleGetBlacklist returns the list of blacklisted BSV21 tokens
// @Summary Get blacklist
// @Description Returns the list of blacklisted topics
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of blacklisted topics"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/blacklist [get]
func (r *Routes) handleGetBlacklist(c *fiber.Ctx) error {
	members, err := r.store.SMembers(c.Context(), bsv21.KeyBlacklist)
	if err != nil {
		r.logger.Error("failed to get blacklist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get blacklist",
		})
	}
	topics := make([]string, len(members))
	for i, m := range members {
		topics[i] = string(m)
	}
	return c.JSON(topics)
}

// handleAddToBlacklist adds a topic to the blacklist
// @Summary Add to blacklist
// @Description Adds a topic to the blacklist
// @Tags admin
// @Accept json
// @Produce json
// @Param body body object true "Topic to add" example({"topic": "tm_example"})
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/blacklist [post]
func (r *Routes) handleAddToBlacklist(c *fiber.Ctx) error {
	var req struct {
		Topic string `json:"topic"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic is required",
		})
	}

	if err := r.store.SAdd(c.Context(), bsv21.KeyBlacklist, []byte(req.Topic)); err != nil {
		r.logger.Error("failed to add to blacklist", "error", err, "topic", req.Topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to add to blacklist",
		})
	}

	r.logger.Info("added topic to blacklist", "topic", req.Topic)
	return c.JSON(fiber.Map{
		"message": "topic added to blacklist",
		"topic":   req.Topic,
	})
}

// handleRemoveFromBlacklist removes a topic from the blacklist
// @Summary Remove from blacklist
// @Description Removes a topic from the blacklist
// @Tags admin
// @Produce json
// @Param topic path string true "Topic ID to remove"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/blacklist/{topic} [delete]
func (r *Routes) handleRemoveFromBlacklist(c *fiber.Ctx) error {
	topic := c.Params("topic")
	if topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic is required",
		})
	}

	if err := r.store.SRem(c.Context(), bsv21.KeyBlacklist, []byte(topic)); err != nil {
		r.logger.Error("failed to remove from blacklist", "error", err, "topic", topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to remove from blacklist",
		})
	}

	r.logger.Info("removed topic from blacklist", "topic", topic)
	return c.JSON(fiber.Map{
		"message": "topic removed from blacklist",
		"topic":   topic,
	})
}

// handleGetActiveTopics returns the list of currently active topics
// @Summary Get active topics
// @Description Returns the list of currently active topics from the overlay engine
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of active topics"
// @Router /admin/topics/active [get]
func (r *Routes) handleGetActiveTopics(c *fiber.Ctx) error {
	if r.overlay == nil {
		return c.JSON([]string{})
	}
	topics := r.overlay.GetTopics()
	if topics == nil {
		topics = []string{}
	}
	return c.JSON(topics)
}

// handleGetActiveLookups returns the list of currently active lookup services
// @Summary Get active lookup services
// @Description Returns the list of currently active lookup services from the overlay engine
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of active lookup services"
// @Router /admin/lookups/active [get]
func (r *Routes) handleGetActiveLookups(c *fiber.Ctx) error {
	if r.overlay == nil {
		return c.JSON([]string{})
	}
	lookups := r.overlay.GetLookupServices()
	if lookups == nil {
		lookups = []string{}
	}
	return c.JSON(lookups)
}

// ProgressItem represents a progress entry
type ProgressItem struct {
	ID    string `json:"id"`
	Block uint32 `json:"block"`
}

// handleGetProgress returns all progress entries from the h:prog hash
// @Summary Get progress
// @Description Returns all sync progress entries (subscriptions, owners, peers)
// @Tags admin
// @Produce json
// @Success 200 {array} ProgressItem "List of progress entries"
// @Router /admin/progress [get]
func (r *Routes) handleGetProgress(c *fiber.Ctx) error {
	if r.store == nil {
		return c.JSON([]ProgressItem{})
	}

	// Get all fields from the h:prog hash
	entries, err := r.store.HGetAll(c.Context(), txo.KeyProgress)
	if err != nil {
		r.logger.Error("failed to get progress", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get progress",
		})
	}

	items := make([]ProgressItem, 0, len(entries))
	for id, valueBytes := range entries {
		var block uint32
		if len(valueBytes) == 4 {
			// Binary uint32 big-endian (used by jbsync and owner sync)
			block = binary.BigEndian.Uint32(valueBytes)
		} else {
			// Try parsing as string (used by peer interactions)
			if parsed, err := strconv.ParseFloat(string(valueBytes), 64); err == nil {
				block = uint32(parsed)
			}
		}
		items = append(items, ProgressItem{
			ID:    id,
			Block: block,
		})
	}

	// Sort by ID
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})

	return c.JSON(items)
}

// handleUpdateProgress updates a progress entry in the h:prog hash
// @Summary Update progress
// @Description Updates a sync progress entry
// @Tags admin
// @Accept json
// @Produce json
// @Param id path string true "Progress ID"
// @Param body body object true "Block height" example({"block": 123456})
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/progress/{id} [put]
func (r *Routes) handleUpdateProgress(c *fiber.Ctx) error {
	if r.store == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "store not available",
		})
	}

	id := c.Params("id")
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "progress ID is required",
		})
	}

	var req struct {
		Block uint32 `json:"block"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Store as binary uint32 big-endian (matches jbsync and owner sync)
	progressBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(progressBytes, req.Block)

	err := r.store.HSet(c.Context(), txo.KeyProgress, []byte(id), progressBytes)
	if err != nil {
		r.logger.Error("failed to update progress", "error", err, "id", id)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update progress",
		})
	}

	r.logger.Info("updated progress", "id", id, "block", req.Block)
	return c.JSON(fiber.Map{
		"message": "progress updated",
		"id":      id,
		"block":   req.Block,
	})
}

// handleDeleteProgress deletes a progress entry from the h:prog hash
// @Summary Delete progress
// @Description Deletes a sync progress entry
// @Tags admin
// @Produce json
// @Param id path string true "Progress ID"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/progress/{id} [delete]
func (r *Routes) handleDeleteProgress(c *fiber.Ctx) error {
	if r.store == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "store not available",
		})
	}

	id := c.Params("id")
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "progress ID is required",
		})
	}

	err := r.store.HDel(c.Context(), txo.KeyProgress, []byte(id))
	if err != nil {
		r.logger.Error("failed to delete progress", "error", err, "id", id)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete progress",
		})
	}

	r.logger.Info("deleted progress", "id", id)
	return c.JSON(fiber.Map{
		"message": "progress deleted",
		"id":      id,
	})
}

// handleGetTopicRemotes returns the remote configuration for a topic
// @Summary Get topic remotes
// @Description Returns the configured remotes for a topic
// @Tags admin
// @Produce json
// @Param name path string true "Topic name"
// @Success 200 {array} overlay.RemoteConfig "List of configured remotes"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/topics/{name}/remotes [get]
func (r *Routes) handleGetTopicRemotes(c *fiber.Ctx) error {
	if r.overlay == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "overlay service not available",
		})
	}

	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic name is required",
		})
	}

	configs, err := r.overlay.GetRemoteConfig(c.Context(), name)
	if err != nil {
		r.logger.Error("failed to get topic remotes", "error", err, "topic", name)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get topic remotes",
		})
	}

	if configs == nil {
		configs = []overlay.RemoteConfig{}
	}

	return c.JSON(configs)
}

// handleSetTopicRemotes sets the remote configuration for a topic
// @Summary Set topic remotes
// @Description Sets the configured remotes for a topic (overrides defaults)
// @Tags admin
// @Accept json
// @Produce json
// @Param name path string true "Topic name"
// @Param body body []overlay.RemoteConfig true "Remote configurations"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/topics/{name}/remotes [put]
func (r *Routes) handleSetTopicRemotes(c *fiber.Ctx) error {
	if r.overlay == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "overlay service not available",
		})
	}

	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic name is required",
		})
	}

	var configs []overlay.RemoteConfig
	if err := c.BodyParser(&configs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if err := r.overlay.SaveRemoteConfig(c.Context(), name, configs); err != nil {
		r.logger.Error("failed to save topic remotes", "error", err, "topic", name)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to save topic remotes",
		})
	}

	r.logger.Info("saved topic remotes", "topic", name, "remotes", len(configs))
	return c.JSON(fiber.Map{
		"message": "topic remotes saved",
		"topic":   name,
		"count":   len(configs),
	})
}

// handleDeleteTopicRemotes deletes the remote configuration for a topic
// @Summary Delete topic remotes
// @Description Removes the remote config override, reverting to defaults
// @Tags admin
// @Produce json
// @Param name path string true "Topic name"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/topics/{name}/remotes [delete]
func (r *Routes) handleDeleteTopicRemotes(c *fiber.Ctx) error {
	if r.overlay == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "overlay service not available",
		})
	}

	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic name is required",
		})
	}

	if err := r.overlay.DeleteRemoteConfig(c.Context(), name); err != nil {
		r.logger.Error("failed to delete topic remotes", "error", err, "topic", name)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete topic remotes",
		})
	}

	r.logger.Info("deleted topic remotes", "topic", name)
	return c.JSON(fiber.Map{
		"message": "topic remotes deleted (reverted to defaults)",
		"topic":   name,
	})
}

// handleGetBSV21Workers returns the status of all active BSV21 token workers
// @Summary Get BSV21 workers
// @Description Returns the status of all active BSV21 token workers
// @Tags admin
// @Produce json
// @Success 200 {array} bsv21.WorkerStatus "List of active workers"
// @Router /admin/bsv21/workers [get]
func (r *Routes) handleGetBSV21Workers(c *fiber.Ctx) error {
	if r.bsv21Sync == nil {
		r.logger.Debug("bsv21 workers: sync service is nil")
		return c.JSON([]bsv21.WorkerStatus{})
	}

	manager := r.bsv21Sync.GetManager()
	if manager == nil {
		r.logger.Debug("bsv21 workers: manager is nil")
		return c.JSON([]bsv21.WorkerStatus{})
	}

	workers := manager.ListWorkers(c.Context())
	if workers == nil {
		workers = []bsv21.WorkerStatus{}
	}

	r.logger.Debug("bsv21 workers", "count", len(workers))

	// Sort by token ID for consistent ordering
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].TokenID < workers[j].TokenID
	})

	return c.JSON(workers)
}
