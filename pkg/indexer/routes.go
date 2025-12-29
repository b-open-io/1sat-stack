package indexer

import (
	"encoding/json"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP routes for the indexer.
type Routes struct {
	pubsub pubsub.PubSub
}

// NewRoutes creates a new Routes instance.
func NewRoutes(ps pubsub.PubSub) *Routes {
	return &Routes{
		pubsub: ps,
	}
}

// RegisterCallback registers the Arc/Arcade webhook callback route.
func (r *Routes) RegisterCallback(router fiber.Router) {
	router.Post("/callback", r.handleCallback)
}

// handleCallback handles Arc/Arcade transaction status callbacks
// @Summary Handle Arc callback
// @Description Receives transaction status updates from Arc/Arcade broadcaster via webhook
// @Tags arc
// @Accept json
// @Produce json
// @Param callback body ArcEvent true "Arc callback payload"
// @Success 200 {object} map[string]string "OK"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /arc/callback [post]
func (r *Routes) handleCallback(c *fiber.Ctx) error {
	var callback ArcEvent
	if err := c.BodyParser(&callback); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if callback.TxID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "txId is required",
		})
	}

	// Publish to internal pubsub for processing by StatusHandler
	if r.pubsub != nil {
		data, err := json.Marshal(callback)
		if err == nil {
			r.pubsub.Publish(c.Context(), "arc", string(data))
		}
	}

	return c.JSON(fiber.Map{
		"status": "ok",
	})
}
