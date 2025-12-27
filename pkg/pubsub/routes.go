package pubsub

import (
	"bufio"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Routes provides HTTP routes for SSE operations
type Routes struct {
	sseManager *SSEManager
}

// NewRoutes creates a new Routes instance
func NewRoutes(sseManager *SSEManager) *Routes {
	return &Routes{sseManager: sseManager}
}

// Register registers routes with a fiber router group
func (r *Routes) Register(router fiber.Router) {
	router.Get("/:topics", r.handleSSE)
}

// handleSSE handles SSE subscriptions
// @Summary Subscribe to Server-Sent Events
// @Description Establishes an SSE connection for real-time event streaming
// @Tags sse
// @Produce text/event-stream
// @Param topics path string true "Comma-separated list of topics to subscribe to"
// @Success 200 {string} string "SSE stream"
// @Router /sse/{topics} [get]
func (r *Routes) handleSSE(c *fiber.Ctx) error {
	topicsParam := c.Params("topics")
	topics := strings.Split(topicsParam, ",")

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		clientID := r.sseManager.RegisterClient(topics, w)
		defer r.sseManager.DeregisterClient(clientID)

		// Keep connection alive until context is done
		<-c.Context().Done()
	})

	return nil
}
