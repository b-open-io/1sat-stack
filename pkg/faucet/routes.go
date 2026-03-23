package faucet

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// Routes handles HTTP route registration for the faucet service.
type Routes struct {
	handlers *Handlers
	logger   *slog.Logger
}

// NewRoutes creates a new Routes instance.
func NewRoutes(svc *Service, logger *slog.Logger) *Routes {
	return &Routes{
		handlers: &Handlers{Svc: svc},
		logger:   logger,
	}
}

// Register registers all faucet routes on the given Fiber router.
// authHandler is the BRC-103/104 auth middleware.
// adminGuard restricts access to admin-only endpoints.
func (r *Routes) Register(router fiber.Router, authHandler fiber.Handler, adminGuard fiber.Handler) {
	h := r.handlers

	// Public endpoints (no auth)
	router.Get("/:slug/status", h.GetFaucetStatus)
	router.Get("/sse-events/:slug/address/:addr", h.DepositEvents)

	// Authenticated endpoints (BRC-103/104)
	authed := router.Group("", authHandler)

	// Faucet CRUD
	authed.Post("/faucets", h.CreateFaucet)

	// Faucet actions
	authed.Post("/:slug/tap", h.TapFaucet)
	authed.Post("/:slug/push", h.PushData)
	authed.Post("/:slug/fund", h.FundTransaction)
	authed.Post("/:slug/mint", h.MintInscription)
	authed.Post("/:slug/actions", h.FaucetActions)

	// Deposit management
	authed.Post("/:slug/deposit-address", h.RequestDepositAddress)
	authed.Post("/:slug/scan-deposits", h.ScanAndIngestDeposits)
	authed.Post("/:slug/sync-addresses", h.SyncFaucetAddresses)

	// Activity
	authed.Post("/:slug/activity", h.GetActivity)
	authed.Get("/:slug/activity-stream", h.ActivityStream)
	authed.Get("/:slug/diagnostics", h.GetDiagnostics)

	// API key management
	authed.Get("/:slug/api-keys", h.ListAPIKeys)
	authed.Post("/:slug/api-keys", h.AddAPIKey)
	authed.Delete("/:slug/api-keys/:publicKey", h.DeleteAPIKey)
	authed.Post("/:slug/generate-api-key", h.GenerateNewAPIKey)

	// Admin endpoints (auth + admin guard)
	admin := router.Group("/admin", authHandler, adminGuard)
	admin.Get("/faucets", h.ListAllFaucets)
	admin.Get("/config/:slug", h.GetFaucetConfig)
	admin.Patch("/config/:slug", h.UpdateFaucetConfig)
	admin.Post("/migrate-owner", h.MigrateOwner)
	admin.Post("/faucets", h.ProvisionFaucet)
}
