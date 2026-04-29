package overlay

import (
	"log/slog"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	overlayserver "github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/gofiber/fiber/v2"
)

// Routes handles overlay HTTP routes for a single module engine.
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

// Register registers overlay routes on a Fiber app group.
// octetStreamLimit caps incoming application/octet-stream bodies (e.g. BEEF
// posts to /submit). Pass the same byte value used for the outer Fiber
// BodyLimit so the two layers stay in sync — RegisterRoutesConfig{} literals
// inherit Go zero-values, not the package defaults, so an unset field would
// reject every submit with "exceeds the maximum allowed size: 0 bytes".
func (r *Routes) Register(group fiber.Router, octetStreamLimit int64) {
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
		OctetStreamLimit: octetStreamLimit,
	})

	// Mount the overlay app
	group.Mount("/", overlayApp)

	r.logger.Debug("registered overlay routes")
}
