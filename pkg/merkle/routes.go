package merkle

import (
	"encoding/json"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP routes for Arc callbacks.
type Routes struct {
	service *Service
	pubsub  pubsub.PubSub
}

// NewRoutes creates a new Routes instance.
func NewRoutes(service *Service, ps pubsub.PubSub) *Routes {
	return &Routes{
		service: service,
		pubsub:  ps,
	}
}

// Register registers routes with a fiber router group.
func (r *Routes) Register(router fiber.Router) {
	router.Post("/callback", r.handleArcCallback)
}

// handleArcCallback handles Arc transaction status callbacks
// @Summary Handle Arc callback
// @Description Receives transaction status updates from Arc broadcaster
// @Tags merkle
// @Accept json
// @Produce json
// @Param callback body ArcCallback true "Arc callback payload"
// @Success 200 {object} map[string]string "OK"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /arc/callback [post]
func (r *Routes) handleArcCallback(c *fiber.Ctx) error {
	var callback ArcCallback
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

	// Publish to internal pubsub for processing
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
