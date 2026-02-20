package wallet

import (
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage/rpcserver"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
)

// Routes holds the wallet HTTP routes.
type Routes struct {
	rpcServer *rpcserver.RPCServer
}

// NewRoutes creates a new Routes instance.
func NewRoutes(rpcServer *rpcserver.RPCServer) *Routes {
	return &Routes{rpcServer: rpcServer}
}

// Register registers the wallet RPC endpoint on the given Fiber router.
// Auth is handled by the global auth middleware; this just mounts the JSON-RPC handler.
func (r *Routes) Register(router fiber.Router) {
	router.Post("/", adaptor.HTTPHandler(r.rpcServer.Handler))
}
