package collections

import (
	_ "embed"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed ui/index.html
var indexHTML []byte

// Routes serves the collections browser.
type Routes struct {
	config *RoutesConfig
	logger *slog.Logger
}

// NewRoutes creates the collections browser routes.
func NewRoutes(cfg *RoutesConfig, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{config: cfg, logger: logger.With("component", "collections-ui")}
}

// Register mounts the browser. Unknown paths return the app shell so
// /collections/{collectionId} can be refreshed.
func (r *Routes) Register(group fiber.Router) {
	group.Get("/", func(c *fiber.Ctx) error {
		if !strings.HasSuffix(c.OriginalURL(), "/") {
			return c.Redirect(c.OriginalURL()+"/", fiber.StatusMovedPermanently)
		}
		return sendIndex(c)
	})
	group.Get("/*", func(c *fiber.Ctx) error {
		return sendIndex(c)
	})
	r.logger.Debug("registered collections browser")
}

func sendIndex(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Send(indexHTML)
}
