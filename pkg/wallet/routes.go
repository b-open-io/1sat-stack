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
	handler  http.Handler // cached handler to share auth context across routes
}

// NewRoutes creates a new Routes instance.
// Uses the wallet service's RPCHandler which doesn't include auth middleware,
// allowing 1sat-stack's global auth middleware to handle authentication.
func NewRoutes(services *Services, logger *slog.Logger) *Routes {
	return &Routes{
		services: services,
		logger:   logger,
		handler:  services.RPCHandler(logger),
	}
}

// Register registers the wallet routes on the given Fiber router.
// The wallet-toolbox RPC server exposes a JSON-RPC endpoint.
// NOTE: We pass the handler directly without stripping prefix because the
// request path is included in the BRC-100 signature. Stripping it would cause
// signature verification to fail.
func (r *Routes) Register(router fiber.Router) {
	// The handler needs to see the full path for signature verification.
	// The wallet-toolbox RPC server registers at POST /{$} which expects root path,
	// but we need requests at /1sat/wallet to work. The trailing slash version
	// maps to the root handler.
	router.All("/", adaptor.HTTPHandler(r.handler))
}

// Handler returns the underlying HTTP handler for manual registration.
// Useful when you need to wrap the handler with additional middleware.
func (r *Routes) Handler() http.Handler {
	return r.handler
}

// RegisterWellKnown registers the /.well-known/auth endpoint at the app root level.
// This is required for BRC-100 authentication handshake, which expects this route
// to be at the root of the server, not under a prefix.
func (r *Routes) RegisterWellKnown(app *fiber.App) {
	// The auth middleware intercepts /.well-known/auth internally, so we pass it through as-is.
	// The underlying handler relies on 1sat-stack's global auth middleware.
	app.All("/.well-known/auth", adaptor.HTTPHandler(r.handler))
}
