package wallet

import (
	"net/http"

	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
)

// Routes holds the wallet HTTP routes.
type Routes struct {
	server  *storage.Server
	handler http.Handler // cached handler to share session manager across routes
}

// NewRoutes creates a new Routes instance.
func NewRoutes(server *storage.Server) *Routes {
	// Cache the handler so we use the same auth middleware (and session manager)
	// across all routes. This is critical for BRC-100 auth to work properly.
	return &Routes{
		server:  server,
		handler: server.Handler(),
	}
}

// Register registers the wallet routes on the given Fiber router.
// The wallet-toolbox server exposes a JSON-RPC endpoint.
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

// RegisterWellKnown registers the /.well-known/auth endpoint at the app root level.
// This is required for BRC-100 authentication handshake, which expects this route
// to be at the root of the server, not under a prefix.
func (r *Routes) RegisterWellKnown(app *fiber.App) {
	// The auth middleware intercepts /.well-known/auth internally, so we pass it through as-is
	app.All("/.well-known/auth", adaptor.HTTPHandler(r.handler))
}
