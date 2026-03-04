package wallet

import (
	"log/slog"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
)

// Routes holds the wallet HTTP routes.
type Routes struct {
	services *Services
	logger   *slog.Logger
	handler  http.Handler // cached handler
}

// NewRoutes creates a new Routes instance.
// Uses the wallet service's RPCHandler which doesn't include auth middleware.
func NewRoutes(services *Services, logger *slog.Logger) *Routes {
	return &Routes{
		services: services,
		logger:   logger,
		handler:  services.RPCHandler(logger),
	}
}

// Register registers the wallet routes on the given Fiber router.
func (r *Routes) Register(router fiber.Router) {
	router.All("/", adaptor.HTTPHandler(r.handler))
}

// Handler returns the underlying HTTP handler.
func (r *Routes) Handler() http.Handler {
	return r.handler
}

// RegisterWellKnown registers the /.well-known/auth endpoint.
func (r *Routes) RegisterWellKnown(app *fiber.App) {
	app.All("/.well-known/auth", adaptor.HTTPHandler(r.handler))
}
