package ovr

import (
	"log/slog"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	overlayserver "github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/gofiber/fiber/v2"
)

// Routes handles overlay HTTP routes
type Routes struct {
	engine *engine.Engine
	config *RoutesConfig
	logger *slog.Logger
}

// NewRoutes creates a new Routes instance
func NewRoutes(eng *engine.Engine, cfg *RoutesConfig, logger *slog.Logger) *Routes {
	return &Routes{
		engine: eng,
		config: cfg,
		logger: logger,
	}
}

// Register registers overlay routes on a Fiber app group
func (r *Routes) Register(group fiber.Router) {
	// Create a sub-app for overlay routes
	overlayApp := fiber.New(fiber.Config{
		ErrorHandler: overlayserver.GetErrorHandler(),
	})

	// Register overlay routes using go-overlay-services
	overlayserver.RegisterRoutes(overlayApp, &overlayserver.RegisterRoutesConfig{
		Engine:           r.engine,
		AdminBearerToken: r.config.AdminBearerToken,
		ARCAPIKey:        r.config.ARCAPIKey,
		ARCCallbackToken: r.config.ARCCallbackToken,
	})

	// Mount the overlay app
	group.Mount("/", overlayApp)

	r.logger.Debug("registered overlay routes")
}
