package admin

import (
	"embed"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"

	"github.com/b-open-io/1sat-stack/pkg/fees"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed ui/*
var uiFS embed.FS

// Routes handles admin HTTP routes
type Routes struct {
	feeService *fees.FeeService
	overlay    *overlay.Services
	store      store.Store
	config     *RoutesConfig
	logger     *slog.Logger
}

// NewRoutes creates a new Routes instance
func NewRoutes(feeService *fees.FeeService, overlaySvc *overlay.Services, s store.Store, cfg *RoutesConfig, logger *slog.Logger) *Routes {
	return &Routes{
		feeService: feeService,
		overlay:    overlaySvc,
		store:      s,
		config:     cfg,
		logger:     logger,
	}
}

// Register registers admin routes on a Fiber app group
func (r *Routes) Register(group fiber.Router) {
	// API routes for topic management
	api := group.Group("/api")

	// Whitelist endpoints
	api.Get("/whitelist", r.handleGetWhitelist)
	api.Post("/whitelist", r.handleAddToWhitelist)
	api.Delete("/whitelist/:topic", r.handleRemoveFromWhitelist)

	// Blacklist endpoints
	api.Get("/blacklist", r.handleGetBlacklist)
	api.Post("/blacklist", r.handleAddToBlacklist)
	api.Delete("/blacklist/:topic", r.handleRemoveFromBlacklist)

	// Active topics endpoint
	api.Get("/topics/active", r.handleGetActiveTopics)

	// Active lookup services endpoint
	api.Get("/lookups/active", r.handleGetActiveLookups)

	// Queue endpoints
	api.Get("/queues", r.handleGetQueues)
	api.Get("/queues/:name", r.handleGetQueueItems)

	// Progress endpoints
	api.Get("/progress", r.handleGetProgress)
	api.Put("/progress/:id", r.handleUpdateProgress)
	api.Delete("/progress/:id", r.handleDeleteProgress)

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

// handleGetWhitelist returns the list of whitelisted topics
// @Summary Get whitelist
// @Description Returns the list of whitelisted topics
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of whitelisted topics"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/api/whitelist [get]
func (r *Routes) handleGetWhitelist(c *fiber.Ctx) error {
	topics, err := r.feeService.GetWhitelist(c.Context())
	if err != nil {
		r.logger.Error("failed to get whitelist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get whitelist",
		})
	}
	return c.JSON(topics)
}

// handleAddToWhitelist adds a topic to the whitelist
// @Summary Add to whitelist
// @Description Adds a topic to the whitelist
// @Tags admin
// @Accept json
// @Produce json
// @Param body body object true "Topic to add" example({"topic": "tm_example"})
// @Success 200 {object} map[string]string "success message"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/api/whitelist [post]
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
			"error": "topic is required",
		})
	}

	if err := r.feeService.AddToWhitelist(c.Context(), req.Topic); err != nil {
		r.logger.Error("failed to add to whitelist", "error", err, "topic", req.Topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to add to whitelist",
		})
	}

	r.logger.Info("added topic to whitelist", "topic", req.Topic)
	return c.JSON(fiber.Map{
		"message": "topic added to whitelist",
		"topic":   req.Topic,
	})
}

// handleRemoveFromWhitelist removes a topic from the whitelist
// @Summary Remove from whitelist
// @Description Removes a topic from the whitelist
// @Tags admin
// @Produce json
// @Param topic path string true "Topic ID to remove"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/api/whitelist/{topic} [delete]
func (r *Routes) handleRemoveFromWhitelist(c *fiber.Ctx) error {
	topic := c.Params("topic")
	if topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic is required",
		})
	}

	if err := r.feeService.RemoveFromWhitelist(c.Context(), topic); err != nil {
		r.logger.Error("failed to remove from whitelist", "error", err, "topic", topic)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to remove from whitelist",
		})
	}

	r.logger.Info("removed topic from whitelist", "topic", topic)
	return c.JSON(fiber.Map{
		"message": "topic removed from whitelist",
		"topic":   topic,
	})
}

// handleGetBlacklist returns the list of blacklisted topics
// @Summary Get blacklist
// @Description Returns the list of blacklisted topics
// @Tags admin
// @Produce json
// @Success 200 {array} string "List of blacklisted topics"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/api/blacklist [get]
func (r *Routes) handleGetBlacklist(c *fiber.Ctx) error {
	topics, err := r.feeService.GetBlacklist(c.Context())
	if err != nil {
		r.logger.Error("failed to get blacklist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get blacklist",
		})
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
// @Router /admin/api/blacklist [post]
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

	if err := r.feeService.AddToBlacklist(c.Context(), req.Topic); err != nil {
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
// @Router /admin/api/blacklist/{topic} [delete]
func (r *Routes) handleRemoveFromBlacklist(c *fiber.Ctx) error {
	topic := c.Params("topic")
	if topic == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "topic is required",
		})
	}

	if err := r.feeService.RemoveFromBlacklist(c.Context(), topic); err != nil {
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
// @Router /admin/api/topics/active [get]
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
// @Router /admin/api/lookups/active [get]
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

// QueueInfo represents queue information
type QueueInfo struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// QueueItem represents an item in a queue
type QueueItem struct {
	Value string  `json:"value"`
	Score float64 `json:"score"`
}

// handleGetQueues returns the list of queues
// @Summary Get queues
// @Description Returns the list of queues (sorted sets with q: prefix)
// @Tags admin
// @Produce json
// @Success 200 {array} QueueInfo "List of queues with counts"
// @Router /admin/api/queues [get]
func (r *Routes) handleGetQueues(c *fiber.Ctx) error {
	if r.store == nil {
		return c.JSON([]QueueInfo{})
	}

	keys, err := r.store.ZKeys(c.Context(), []byte("q:"))
	if err != nil {
		r.logger.Error("failed to get queue keys", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get queues",
		})
	}

	queues := make([]QueueInfo, 0, len(keys))
	for _, key := range keys {
		count, err := r.store.ZCard(c.Context(), []byte(key))
		if err != nil {
			r.logger.Warn("failed to get queue count", "key", key, "error", err)
			count = 0
		}
		queues = append(queues, QueueInfo{
			Name:  key,
			Count: count,
		})
	}

	// Sort by name
	sort.Slice(queues, func(i, j int) bool {
		return queues[i].Name < queues[j].Name
	})

	return c.JSON(queues)
}

// handleGetQueueItems returns items from a specific queue
// @Summary Get queue items
// @Description Returns the first 25 items from a queue
// @Tags admin
// @Produce json
// @Param name path string true "Queue name"
// @Success 200 {array} QueueItem "List of queue items"
// @Router /admin/api/queues/{name} [get]
func (r *Routes) handleGetQueueItems(c *fiber.Ctx) error {
	if r.store == nil {
		return c.JSON([]QueueItem{})
	}

	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "queue name is required",
		})
	}

	members, err := r.store.ZRange(c.Context(), []byte(name), store.ScoreRange{
		Count: 25,
	})
	if err != nil {
		r.logger.Error("failed to get queue items", "queue", name, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get queue items",
		})
	}

	items := make([]QueueItem, 0, len(members))
	for _, m := range members {
		var value string
		switch len(m.Member) {
		case 32:
			// chainhash.Hash
			hash, err := chainhash.NewHash(m.Member)
			if err == nil {
				value = hash.String()
			} else {
				value = hex.EncodeToString(m.Member)
			}
		case 36:
			// transaction.Outpoint
			op := types.NewOutpointFromBytes(m.Member)
			if op != nil {
				value = op.String()
			} else {
				value = hex.EncodeToString(m.Member)
			}
		default:
			value = hex.EncodeToString(m.Member)
		}
		items = append(items, QueueItem{
			Value: value,
			Score: m.Score,
		})
	}

	return c.JSON(items)
}

// ProgressKey is the key for sync progress tracking
const ProgressKey = "sync:progress"

// ProgressItem represents a progress entry
type ProgressItem struct {
	ID    string  `json:"id"`
	Block float64 `json:"block"`
}

// handleGetProgress returns all progress entries
// @Summary Get progress
// @Description Returns all sync progress entries
// @Tags admin
// @Produce json
// @Success 200 {array} ProgressItem "List of progress entries"
// @Router /admin/api/progress [get]
func (r *Routes) handleGetProgress(c *fiber.Ctx) error {
	if r.store == nil {
		return c.JSON([]ProgressItem{})
	}

	members, err := r.store.ZRange(c.Context(), []byte(ProgressKey), store.ScoreRange{})
	if err != nil {
		r.logger.Error("failed to get progress", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get progress",
		})
	}

	items := make([]ProgressItem, 0, len(members))
	for _, m := range members {
		items = append(items, ProgressItem{
			ID:    string(m.Member),
			Block: m.Score,
		})
	}

	// Sort by ID
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})

	return c.JSON(items)
}

// handleUpdateProgress updates a progress entry
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
// @Router /admin/api/progress/{id} [put]
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
		Block float64 `json:"block"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	err := r.store.ZAdd(c.Context(), []byte(ProgressKey), store.ScoredMember{
		Member: []byte(id),
		Score:  req.Block,
	})
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

// handleDeleteProgress deletes a progress entry
// @Summary Delete progress
// @Description Deletes a sync progress entry
// @Tags admin
// @Produce json
// @Param id path string true "Progress ID"
// @Success 200 {object} map[string]string "success message"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /admin/api/progress/{id} [delete]
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

	err := r.store.ZRem(c.Context(), []byte(ProgressKey), []byte(id))
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
