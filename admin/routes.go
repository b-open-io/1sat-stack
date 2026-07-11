package admin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/auth"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed ui/dist/*
var uiFS embed.FS

// Routes handles admin HTTP routes
type Routes struct {
	overlay          *overlay.Services
	engines          map[string]*engine.Engine
	store            store.Store
	configStore      config.Store
	bsv21Sync        *bsv21.SyncServices
	triggerOpnsCrawl OpnsCrawlFunc
	requestRestart   func()
	config           *RoutesConfig
	logger           *slog.Logger
	logStore         *logging.SQLiteHandler
}

// TopicRequest is the request body for adding a token/topic to whitelist or blacklist
type TopicRequest struct {
	Topic string `json:"topic" example:"bsv21_token_id_here"`
}

// UpdateProgressRequest is the request body for updating a progress entry
type UpdateProgressRequest struct {
	Block uint32 `json:"block" example:"123456"`
}

// NewRoutes creates a new Routes instance
func NewRoutes(overlaySvc *overlay.Services, engines map[string]*engine.Engine, s store.Store, cs config.Store, bsv21Sync *bsv21.SyncServices, triggerCrawl OpnsCrawlFunc, requestRestart func(), cfg *RoutesConfig, logger *slog.Logger, logStore *logging.SQLiteHandler) *Routes {
	return &Routes{
		overlay:          overlaySvc,
		engines:          engines,
		store:            s,
		configStore:      cs,
		bsv21Sync:        bsv21Sync,
		triggerOpnsCrawl: triggerCrawl,
		requestRestart:   requestRestart,
		config:           cfg,
		logger:           logger,
		logStore:         logStore,
	}
}

// Register registers admin API routes on a guarded group and static UI files
// on an unguarded group so the browser can load the app before authenticating.
// The authHandler extracts the caller's identity via BRC-103/104 and is applied
// only to POST /setup (which needs identity but not AdminGuard).
func (r *Routes) Register(guardedGroup fiber.Router, publicGroup fiber.Router, authHandler fiber.Handler) {
	// Setup routes
	publicGroup.Get("/setup/status", r.handleGetSetupStatus)
	publicGroup.Post("/setup", authHandler, r.handleSetup)
	publicGroup.Post("/setup/request", authHandler, r.handleRequestAccess)
	publicGroup.Get("/setup/check", authHandler, r.handleCheckAccess)
	publicGroup.Post("/setup/complete", r.handleSetupComplete)
	publicGroup.Get("/setup/config", r.handleGetSetupConfig)

	// API routes (require auth via AdminGuard on guardedGroup)
	guardedGroup.Get("/whitelist", r.handleGetWhitelist)
	guardedGroup.Post("/whitelist", r.handleAddToWhitelist)
	guardedGroup.Delete("/whitelist/:token", r.handleRemoveFromWhitelist)

	guardedGroup.Get("/blacklist", r.handleGetBlacklist)
	guardedGroup.Post("/blacklist", r.handleAddToBlacklist)
	guardedGroup.Delete("/blacklist/:token", r.handleRemoveFromBlacklist)

	guardedGroup.Get("/topics/active", r.handleGetActiveTopics)

	guardedGroup.Get("/topics/:name/remotes", r.handleGetTopicRemotes)
	guardedGroup.Put("/topics/:name/remotes", r.handleSetTopicRemotes)
	guardedGroup.Delete("/topics/:name/remotes", r.handleDeleteTopicRemotes)

	guardedGroup.Get("/lookups/active", r.handleGetActiveLookups)

	guardedGroup.Get("/progress", r.handleGetProgress)
	guardedGroup.Put("/progress/:id", r.handleUpdateProgress)
	guardedGroup.Delete("/progress/:id", r.handleDeleteProgress)

	guardedGroup.Get("/bsv21/workers", r.handleGetBSV21Workers)

	guardedGroup.Get("/users", r.handleGetUsers)
	guardedGroup.Post("/users", r.handleAddUser)
	guardedGroup.Put("/users/:pubkey", r.handleUpdateUser)
	guardedGroup.Delete("/users/:pubkey", r.handleDeleteUser)

	guardedGroup.Get("/requests", r.handleGetRequests)
	guardedGroup.Post("/requests/:pubkey/approve", r.handleApproveRequest)
	guardedGroup.Delete("/requests/:pubkey", r.handleDenyRequest)

	guardedGroup.Post("/opns/crawl", r.handleTriggerOpnsCrawl)

	guardedGroup.Post("/restart", r.handleRestart)

	guardedGroup.Get("/config", r.handleGetConfig)
	guardedGroup.Put("/config", r.handleUpdateConfig)

	guardedGroup.Get("/logs", r.handleQueryLogs)
	guardedGroup.Get("/logs/components", r.handleQueryLogComponents)

	dataRoutes := NewDataRoutes(r.store, r.logger)
	dataRoutes.Register(guardedGroup.Group("/data"))

	// Static UI files (no auth required — the UI itself handles authentication)
	uiSubFS, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		r.logger.Error("failed to create ui sub filesystem", "error", err)
		return
	}

	publicGroup.Get("/", func(c *fiber.Ctx) error {
		if !strings.HasSuffix(c.OriginalURL(), "/") {
			return c.Redirect(c.OriginalURL()+"/", fiber.StatusMovedPermanently)
		}
		content, err := fs.ReadFile(uiSubFS, "index.html")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("Not found")
		}
		c.Set("Content-Type", "text/html")
		return c.Send(content)
	})

	publicGroup.Use("/", filesystem.New(filesystem.Config{
		Root:   http.FS(uiSubFS),
		Browse: false,
	}))

	// SPA fallback: serve index.html for any unmatched routes (client-side routing)
	publicGroup.Get("/*", func(c *fiber.Ctx) error {
		content, err := fs.ReadFile(uiSubFS, "index.html")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("Not found")
		}
		c.Set("Content-Type", "text/html")
		return c.Send(content)
	})

	r.logger.Debug("registered admin routes")
}

// handleGetWhitelist returns the list of whitelisted BSV21 tokens
// @Summary Get whitelist
// @Description Returns the list of whitelisted BSV21 tokens (always active)
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of whitelisted tokens"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/whitelist [get]
func (r *Routes) handleGetWhitelist(c *fiber.Ctx) error {
	entries, err := r.configStore.List(c.Context(), "bsv21.whitelist:")
	if err != nil {
		r.logger.Error("failed to get whitelist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get whitelist",
		})
	}
	tokens := make([]string, 0, len(entries))
	for key := range entries {
		tokens = append(tokens, strings.TrimPrefix(key, "bsv21.whitelist:"))
	}
	sort.Strings(tokens)
	return c.JSON(tokens)
}

// handleAddToWhitelist adds a token to the whitelist
// @Summary Add to whitelist
// @Description Adds a BSV21 token to the whitelist (always active)
// @Tags admin
// @Accept json
// @Produce json
// @Param body body TopicRequest true "Token to add"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/whitelist [post]
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

	if err := r.configStore.Set(c.Context(), "bsv21.whitelist:"+req.Topic, "1"); err != nil {
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
// @Security BearerAuth
// @Router /api/whitelist/{token} [delete]
func (r *Routes) handleRemoveFromWhitelist(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "token is required",
		})
	}

	if err := r.configStore.Delete(c.Context(), "bsv21.whitelist:"+token); err != nil {
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
// @Security BearerAuth
// @Router /api/blacklist [get]
func (r *Routes) handleGetBlacklist(c *fiber.Ctx) error {
	entries, err := r.configStore.List(c.Context(), "bsv21.blacklist:")
	if err != nil {
		r.logger.Error("failed to get blacklist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get blacklist",
		})
	}
	topics := make([]string, 0, len(entries))
	for key := range entries {
		topics = append(topics, strings.TrimPrefix(key, "bsv21.blacklist:"))
	}
	sort.Strings(topics)
	return c.JSON(topics)
}

// handleAddToBlacklist adds a topic to the blacklist
// @Summary Add to blacklist
// @Description Adds a topic to the blacklist
// @Tags admin
// @Accept json
// @Produce json
// @Param body body TopicRequest true "Topic to add"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/blacklist [post]
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

	if err := r.configStore.Set(c.Context(), "bsv21.blacklist:"+req.Topic, "1"); err != nil {
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
// @Security BearerAuth
// @Router /api/blacklist/{topic} [delete]
func (r *Routes) handleRemoveFromBlacklist(c *fiber.Ctx) error {
	topic := c.Params("topic")
	if topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic is required",
		})
	}

	if err := r.configStore.Delete(c.Context(), "bsv21.blacklist:"+topic); err != nil {
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
// @Security BearerAuth
// @Router /api/topics/active [get]
func (r *Routes) handleGetActiveTopics(c *fiber.Ctx) error {
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

// handleGetActiveLookups returns the list of currently active lookup services
// @Summary Get active lookup services
// @Description Returns the list of currently active lookup services from the overlay engine
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of active lookup services"
// @Security BearerAuth
// @Router /api/lookups/active [get]
func (r *Routes) handleGetActiveLookups(c *fiber.Ctx) error {
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
// @Security BearerAuth
// @Router /api/progress [get]
func (r *Routes) handleGetProgress(c *fiber.Ctx) error {
	entries, err := r.configStore.List(c.Context(), "progress:")
	if err != nil {
		r.logger.Error("failed to get progress", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get progress",
		})
	}

	items := make([]ProgressItem, 0, len(entries))
	for key, val := range entries {
		var block uint32
		if parsed, err := strconv.ParseUint(val, 10, 32); err == nil {
			block = uint32(parsed)
		}
		items = append(items, ProgressItem{
			ID:    strings.TrimPrefix(key, "progress:"),
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
// @Param body body UpdateProgressRequest true "Block height"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/progress/{id} [put]
func (r *Routes) handleUpdateProgress(c *fiber.Ctx) error {
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

	if err := r.configStore.Set(c.Context(), "progress:"+id, strconv.FormatUint(uint64(req.Block), 10)); err != nil {
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
// @Security BearerAuth
// @Router /api/progress/{id} [delete]
func (r *Routes) handleDeleteProgress(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "progress ID is required",
		})
	}

	if err := r.configStore.Delete(c.Context(), "progress:"+id); err != nil {
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
// @Security BearerAuth
// @Router /api/topics/{name}/remotes [get]
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
// @Security BearerAuth
// @Router /api/topics/{name}/remotes [put]
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
// @Security BearerAuth
// @Router /api/topics/{name}/remotes [delete]
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
// @Security BearerAuth
// @Router /api/bsv21/workers [get]
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

// handleGetSetupStatus returns whether the admin has been configured.
// @Summary Get setup status
// @Description Returns whether any admin identities have been configured
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]bool "configured status"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /setup/status [get]
func (r *Routes) handleGetSetupStatus(c *fiber.Ctx) error {
	configured, err := auth.IsSetup(c.Context(), r.configStore)
	if err != nil {
		r.logger.Error("failed to check setup status", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}
	return c.JSON(fiber.Map{
		"configured": configured,
	})
}

// handleSetup performs first-run admin setup by adding the authenticated
// identity as the initial admin. Rejects if already configured.
// @Summary Perform admin setup
// @Description Adds the authenticated identity as the first admin
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]string "success message"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 409 {object} map[string]string "Already configured"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /setup [post]
func (r *Routes) handleSetup(c *fiber.Ctx) error {
	identity := auth.GetIdentity(c)
	if identity == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "unauthorized",
		})
	}

	configured, err := auth.IsSetup(c.Context(), r.configStore)
	if err != nil {
		r.logger.Error("failed to check setup status", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}
	if configured {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "admin already configured",
		})
	}

	user := auth.AdminUser{
		Pubkey: identity.ToDERHex(),
		Admin:  true,
	}
	if err := auth.SaveAdminUser(c.Context(), r.configStore, user); err != nil {
		r.logger.Error("failed to add initial admin", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}

	r.logger.Info("admin setup complete", "pubkey", user.Pubkey)
	return c.JSON(fiber.Map{
		"message": "admin configured",
	})
}

// handleGetUsers returns all configured admin users.
// @Summary List users
// @Description Returns all configured admin users
// @Tags admin
// @Produce json
// @Success 200 {array} auth.AdminUser "List of users"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/users [get]
func (r *Routes) handleGetUsers(c *fiber.Ctx) error {
	users, err := auth.ListAdminUsers(c.Context(), r.configStore)
	if err != nil {
		r.logger.Error("failed to list users", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list users",
		})
	}
	return c.JSON(users)
}

// handleAddUser adds an admin user.
// @Summary Add user
// @Description Adds a user identified by pubkey
// @Tags admin
// @Accept json
// @Produce json
// @Param body body auth.AdminUser true "User to add"
// @Success 200 {object} auth.AdminUser "Added user"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/users [post]
func (r *Routes) handleAddUser(c *fiber.Ctx) error {
	var user auth.AdminUser
	if err := c.BodyParser(&user); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if user.Pubkey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pubkey is required",
		})
	}

	if err := auth.SaveAdminUser(c.Context(), r.configStore, user); err != nil {
		r.logger.Error("failed to add user", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to add user",
		})
	}

	r.logger.Info("added user", "pubkey", user.Pubkey, "name", user.Name, "admin", user.Admin)
	return c.JSON(user)
}

// handleUpdateUser updates a user's name and/or admin flag.
// @Summary Update user
// @Description Updates the name and/or admin flag of an existing user
// @Tags admin
// @Accept json
// @Produce json
// @Param pubkey path string true "User pubkey (DER hex)"
// @Param body body object{name=string,admin=bool} true "Fields to update"
// @Success 200 {object} auth.AdminUser "Updated user"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "User not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/users/{pubkey} [put]
func (r *Routes) handleUpdateUser(c *fiber.Ctx) error {
	pubkey := c.Params("pubkey")
	if pubkey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pubkey is required",
		})
	}

	existing, err := auth.GetAdminUser(c.Context(), r.configStore, pubkey)
	if err != nil {
		r.logger.Error("failed to get user", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get user",
		})
	}
	if existing == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user not found",
		})
	}

	var updates map[string]json.RawMessage
	if err := c.BodyParser(&updates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if v, ok := updates["name"]; ok {
		var name string
		if err := json.Unmarshal(v, &name); err == nil {
			existing.Name = name
		}
	}
	if v, ok := updates["admin"]; ok {
		var admin bool
		if err := json.Unmarshal(v, &admin); err == nil {
			existing.Admin = admin
		}
	}

	if err := auth.SaveAdminUser(c.Context(), r.configStore, *existing); err != nil {
		r.logger.Error("failed to update user", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update user",
		})
	}

	r.logger.Info("updated user", "pubkey", pubkey, "name", existing.Name, "admin", existing.Admin)
	return c.JSON(existing)
}

// handleDeleteUser removes an admin user.
// @Summary Delete user
// @Description Removes a user by pubkey
// @Tags admin
// @Produce json
// @Param pubkey path string true "User pubkey (DER hex)"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/users/{pubkey} [delete]
func (r *Routes) handleDeleteUser(c *fiber.Ctx) error {
	pubkey := c.Params("pubkey")
	if pubkey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pubkey is required",
		})
	}

	if err := auth.DeleteAdminUser(c.Context(), r.configStore, pubkey); err != nil {
		r.logger.Error("failed to delete user", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete user",
		})
	}

	r.logger.Info("deleted user", "pubkey", pubkey)
	return c.JSON(fiber.Map{
		"message": "user deleted",
	})
}

// handleCheckAccess reports the authenticated identity's access status.
// @Summary Check access
// @Description Returns whether the authenticated identity is an approved user
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]interface{} "status and admin flag"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /setup/check [get]
func (r *Routes) handleCheckAccess(c *fiber.Ctx) error {
	identity := auth.GetIdentity(c)
	if identity == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "unauthorized",
		})
	}

	pubkey := identity.ToDERHex()
	user, err := auth.GetAdminUser(c.Context(), r.configStore, pubkey)
	if err != nil {
		r.logger.Error("failed to check access", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}

	if user != nil {
		return c.JSON(fiber.Map{
			"status": "approved",
			"admin":  user.Admin,
		})
	}

	return c.JSON(fiber.Map{
		"status": "none",
	})
}

// handleRequestAccess submits an access request for the authenticated identity.
// @Summary Request access
// @Description Submits an access request for the authenticated identity
// @Tags admin
// @Accept json
// @Produce json
// @Param body body object{name=string} true "Requester name"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /setup/request [post]
func (r *Routes) handleRequestAccess(c *fiber.Ctx) error {
	identity := auth.GetIdentity(c)
	if identity == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "unauthorized",
		})
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	accessReq := auth.AccessRequest{
		Pubkey: identity.ToDERHex(),
		Name:   req.Name,
	}
	if err := auth.SaveAccessRequest(c.Context(), r.configStore, accessReq); err != nil {
		r.logger.Error("failed to save access request", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to save request",
		})
	}

	r.logger.Info("access request submitted", "pubkey", accessReq.Pubkey, "name", accessReq.Name)
	return c.JSON(fiber.Map{
		"message": "access request submitted",
	})
}

// handleGetRequests returns pending access requests.
// @Summary List access requests
// @Description Returns pending access requests
// @Tags admin
// @Produce json
// @Success 200 {array} auth.AccessRequest "List of pending requests"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/requests [get]
func (r *Routes) handleGetRequests(c *fiber.Ctx) error {
	requests, err := auth.ListAccessRequests(c.Context(), r.configStore)
	if err != nil {
		r.logger.Error("failed to list requests", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list requests",
		})
	}
	return c.JSON(requests)
}

// handleApproveRequest approves an access request, creating a user.
// @Summary Approve access request
// @Description Approves a pending access request and creates the user
// @Tags admin
// @Accept json
// @Produce json
// @Param pubkey path string true "Requester pubkey (DER hex)"
// @Param body body object{admin=bool} false "Grant admin rights"
// @Success 200 {object} auth.AdminUser "Created user"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/requests/{pubkey}/approve [post]
func (r *Routes) handleApproveRequest(c *fiber.Ctx) error {
	pubkey := c.Params("pubkey")
	if pubkey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pubkey is required",
		})
	}

	var req struct {
		Admin bool `json:"admin"`
	}
	if err := c.BodyParser(&req); err != nil {
		req.Admin = false
	}

	// Get the request to preserve the name
	requests, err := auth.ListAccessRequests(c.Context(), r.configStore)
	if err != nil {
		r.logger.Error("failed to list requests", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}

	var name string
	for _, r := range requests {
		if r.Pubkey == pubkey {
			name = r.Name
			break
		}
	}

	user := auth.AdminUser{
		Pubkey: pubkey,
		Name:   name,
		Admin:  req.Admin,
	}
	if err := auth.SaveAdminUser(c.Context(), r.configStore, user); err != nil {
		r.logger.Error("failed to approve request", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to approve request",
		})
	}

	if err := auth.DeleteAccessRequest(c.Context(), r.configStore, pubkey); err != nil {
		r.logger.Error("failed to delete request after approval", "error", err)
	}

	r.logger.Info("approved access request", "pubkey", pubkey, "name", name, "admin", req.Admin)
	return c.JSON(user)
}

// handleDenyRequest deletes a pending access request.
// @Summary Deny access request
// @Description Deletes a pending access request
// @Tags admin
// @Produce json
// @Param pubkey path string true "Requester pubkey (DER hex)"
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/requests/{pubkey} [delete]
func (r *Routes) handleDenyRequest(c *fiber.Ctx) error {
	pubkey := c.Params("pubkey")
	if pubkey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pubkey is required",
		})
	}

	if err := auth.DeleteAccessRequest(c.Context(), r.configStore, pubkey); err != nil {
		r.logger.Error("failed to deny request", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to deny request",
		})
	}

	r.logger.Info("denied access request", "pubkey", pubkey)
	return c.JSON(fiber.Map{
		"message": "request denied",
	})
}

// SetupCompleteRequest is the request body for completing the setup wizard.
type SetupCompleteRequest struct {
	AuthMode           string `json:"authMode"`
	WIF                string `json:"wif"`
	AdminIdentityKey   string `json:"adminIdentityKey"`
	ChaintracksBackend string `json:"chaintracksBackend"`
	ChaintracksPath    string `json:"chaintracksPath"`
	ChaintracksUrl     string `json:"chaintracksUrl"`
	ArcadeBackend      string `json:"arcadeBackend"`
	ArcadePath         string `json:"arcadePath"`
	ArcadeUrl          string `json:"arcadeUrl"`
	MessageboxUrl      string `json:"messageboxUrl"`
}

// handleSetupComplete writes the full setup wizard configuration to the config store.
// @Summary Complete setup wizard
// @Description Writes initial configuration from the setup wizard
// @Tags admin
// @Accept json
// @Produce json
// @Param body body SetupCompleteRequest true "Setup configuration"
// @Success 200 {object} map[string]string "status and restart flag"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /setup/complete [post]
func (r *Routes) handleSetupComplete(c *fiber.Ctx) error {
	var req SetupCompleteRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	ctx := c.Context()

	// Always write auth mode
	if err := r.configStore.Set(ctx, "auth.mode", req.AuthMode); err != nil {
		r.logger.Error("failed to write auth mode", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to write configuration",
		})
	}

	// Only write config keys if the user changed them from defaults
	overrides := map[string]struct{ value, defaultVal string }{
		"chaintracks.mode": {req.ChaintracksBackend, "embedded"},
		"chaintracks.path": {req.ChaintracksPath, "chaintracks"},
		"chaintracks.url":  {req.ChaintracksUrl, ""},
		"arcade.mode":      {req.ArcadeBackend, "embedded"},
		"arcade.path":      {req.ArcadePath, "arcade/arcade.db"},
		"arcade.url":       {req.ArcadeUrl, ""},
		"messagebox_url":   {req.MessageboxUrl, ""},
	}

	for key, o := range overrides {
		if o.value != o.defaultVal {
			if err := r.configStore.Set(ctx, key, o.value); err != nil {
				r.logger.Error("failed to write setup config", "error", err, "key", key)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "failed to write configuration",
				})
			}
		}
	}

	if req.AuthMode == "authenticated" && req.AdminIdentityKey != "" {
		if err := auth.SaveAdminUser(ctx, r.configStore, auth.AdminUser{
			Pubkey: req.AdminIdentityKey,
			Name:   "Admin",
			Admin:  true,
		}); err != nil {
			r.logger.Error("failed to save admin user during setup", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to save admin user",
			})
		}
	}

	if req.AuthMode == "local" && req.WIF != "" {
		if err := r.configStore.Set(ctx, "server.private_key", req.WIF); err != nil {
			r.logger.Error("failed to write private key during setup", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to write private key",
			})
		}
	}

	if err := r.configStore.Set(ctx, "setup.complete", "true"); err != nil {
		r.logger.Error("failed to mark setup complete", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to mark setup complete",
		})
	}

	r.logger.Info("setup wizard completed", "authMode", req.AuthMode)
	return c.JSON(fiber.Map{
		"status":  "complete",
		"restart": true,
	})
}

// SetupConfigResponse is the response for GET /setup/config.
type SetupConfigResponse struct {
	AuthMode           string `json:"authMode"`
	ChaintracksBackend string `json:"chaintracksBackend"`
	ChaintracksPath    string `json:"chaintracksPath"`
	ChaintracksUrl     string `json:"chaintracksUrl"`
	ArcadeBackend      string `json:"arcadeBackend"`
	ArcadePath         string `json:"arcadePath"`
	ArcadeUrl          string `json:"arcadeUrl"`
	MessageboxUrl      string `json:"messageboxUrl"`
}

// handleGetSetupConfig returns the current setup configuration from the config store.
// @Summary Get setup configuration
// @Description Returns current setup configuration for the wizard UI
// @Tags admin
// @Produce json
// @Success 200 {object} SetupConfigResponse "Current setup configuration"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /setup/config [get]
func (r *Routes) handleGetSetupConfig(c *fiber.Ctx) error {
	ctx := c.Context()

	keys := map[string]*string{
		"auth.mode":        new(string),
		"chaintracks.mode": new(string),
		"chaintracks.path": new(string),
		"chaintracks.url":  new(string),
		"arcade.mode":      new(string),
		"arcade.path":      new(string),
		"arcade.url":       new(string),
		"messagebox_url":   new(string),
	}

	for key, dest := range keys {
		val, err := r.configStore.Get(ctx, key)
		if err != nil && !errors.Is(err, config.ErrNotFound) {
			r.logger.Error("failed to read setup config", "error", err, "key", key)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to read configuration",
			})
		}
		*dest = val
	}

	return c.JSON(SetupConfigResponse{
		AuthMode:           *keys["auth.mode"],
		ChaintracksBackend: *keys["chaintracks.mode"],
		ChaintracksPath:    *keys["chaintracks.path"],
		ChaintracksUrl:     *keys["chaintracks.url"],
		ArcadeBackend:      *keys["arcade.mode"],
		ArcadePath:         *keys["arcade.path"],
		ArcadeUrl:          *keys["arcade.url"],
		MessageboxUrl:      *keys["messagebox_url"],
	})
}

// handleGetConfig returns all config store entries as a flat JSON object.
// @Summary Get all config
// @Description Returns all current config values from the config store
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]string "All config key-value pairs"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/config [get]
func (r *Routes) handleGetConfig(c *fiber.Ctx) error {
	entries, err := r.configStore.List(c.Context(), "")
	if err != nil {
		r.logger.Error("failed to list config", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list config",
		})
	}
	if entries == nil {
		entries = map[string]string{}
	}
	return c.JSON(entries)
}

// handleUpdateConfig writes config key-value pairs to the config store.
// @Summary Update config
// @Description Writes a flat JSON object of key-value pairs to the config store. Empty values delete the key.
// @Tags admin
// @Accept json
// @Produce json
// @Param body body map[string]string true "Config key-value pairs"
// @Success 200 {object} map[string]interface{} "status and count of updated keys"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/config [put]
func (r *Routes) handleUpdateConfig(c *fiber.Ctx) error {
	var updates map[string]string
	if err := c.BodyParser(&updates); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	ctx := c.Context()
	var updated int

	for key, value := range updates {
		if key == "setup.complete" || strings.HasPrefix(key, "user:") {
			r.logger.Warn("rejected config write to protected key", "key", key)
			continue
		}

		if value == "" {
			if err := r.configStore.Delete(ctx, key); err != nil {
				r.logger.Error("failed to delete config key", "error", err, "key", key)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "failed to delete config key: " + key,
				})
			}
		} else {
			if err := r.configStore.Set(ctx, key, value); err != nil {
				r.logger.Error("failed to set config key", "error", err, "key", key)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "failed to set config key: " + key,
				})
			}
		}
		updated++
	}

	// Sync JSON array keys to individual prefixed keys for whitelist/blacklist
	for _, mapping := range []struct{ jsonKey, prefix string }{
		{"overlay.bsv21.whitelist", "bsv21.whitelist:"},
		{"overlay.bsv21.blacklist", "bsv21.blacklist:"},
	} {
		if jsonVal, ok := updates[mapping.jsonKey]; ok {
			if err := r.syncPrefixedKeys(ctx, mapping.prefix, jsonVal); err != nil {
				r.logger.Error("failed to sync prefixed keys", "error", err, "prefix", mapping.prefix)
			}
		}
	}

	r.logger.Info("config updated", "keys", updated)
	return c.JSON(fiber.Map{
		"status":  "ok",
		"updated": updated,
	})
}

// syncPrefixedKeys replaces all keys with the given prefix to match the JSON array value.
// Existing prefixed keys not in the array are deleted; new entries are added.
func (r *Routes) syncPrefixedKeys(ctx context.Context, prefix string, jsonVal string) error {
	var desired []string
	if err := json.Unmarshal([]byte(jsonVal), &desired); err != nil {
		return err
	}

	desiredSet := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		desiredSet[id] = struct{}{}
	}

	existing, err := r.configStore.List(ctx, prefix)
	if err != nil {
		return err
	}

	// Delete keys that are no longer in the desired set
	for key := range existing {
		id := strings.TrimPrefix(key, prefix)
		if _, ok := desiredSet[id]; !ok {
			if err := r.configStore.Delete(ctx, key); err != nil {
				return err
			}
		}
	}

	// Add keys that are new
	for _, id := range desired {
		key := prefix + id
		if _, exists := existing[key]; !exists {
			if err := r.configStore.Set(ctx, key, "1"); err != nil {
				return err
			}
		}
	}

	return nil
}

// handleTriggerOpnsCrawl triggers the OpNS genesis crawl.
// @Summary Trigger OpNS crawl
// @Description Starts the OpNS genesis crawl
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "OpNS crawl not available"
// @Security BearerAuth
// @Router /api/opns/crawl [post]
func (r *Routes) handleTriggerOpnsCrawl(c *fiber.Ctx) error {
	if r.triggerOpnsCrawl == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "OpNS crawl not available — ensure opns and beef services are enabled",
		})
	}

	if err := r.triggerOpnsCrawl(c.Context()); err != nil {
		r.logger.Error("failed to trigger OpNS crawl", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "OpNS crawl started",
	})
}

// handleRestart triggers a server self-restart via syscall.Exec.
// @Summary Restart server
// @Description Triggers a server restart by re-executing the current binary
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]string "restarting status"
// @Failure 503 {object} map[string]string "restart not available"
// @Security BearerAuth
// @Router /api/restart [post]
func (r *Routes) handleRestart(c *fiber.Ctx) error {
	if r.requestRestart == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "restart not available",
		})
	}

	r.logger.Info("server restart requested")
	r.requestRestart()
	return c.JSON(fiber.Map{
		"status": "restarting",
	})
}
