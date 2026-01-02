package wallet

import (
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
)

// Routes holds the wallet HTTP routes.
type Routes struct {
	server *storage.Server
}

// NewRoutes creates a new Routes instance.
func NewRoutes(server *storage.Server) *Routes {
	return &Routes{
		server: server,
	}
}

// Register registers the wallet routes on the given Fiber router.
// The wallet-toolbox server exposes a JSON-RPC endpoint, which we mount
// using Fiber's adaptor to convert the http.Handler.
func (r *Routes) Register(router fiber.Router) {
	// Mount the http.Handler from wallet-toolbox using Fiber's adaptor
	// All requests to this group will be handled by the wallet JSON-RPC server
	router.All("/*", adaptor.HTTPHandler(r.server.Handler()))
}
