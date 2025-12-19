package overlay

import (
	"log/slog"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	overlayserver "github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/gofiber/fiber/v2"
)

// The following are swagger documentation stubs for overlay routes.
// The actual handlers are provided by go-overlay-services.

// listTopicManagers lists all registered topic managers
// @Summary List topic managers
// @Description Returns a list of all registered topic managers with their metadata
// @Tags overlay
// @Produce json
// @Success 200 {object} map[string]interface{} "Map of topic manager names to metadata"
// @Router /overlay/listTopicManagers [get]
func listTopicManagers() {}

// listLookupServiceProviders lists all registered lookup service providers
// @Summary List lookup service providers
// @Description Returns a list of all registered lookup service providers with their metadata
// @Tags overlay
// @Produce json
// @Success 200 {object} map[string]interface{} "Map of lookup service names to metadata"
// @Router /overlay/listLookupServiceProviders [get]
func listLookupServiceProviders() {}

// submitTransaction submits a transaction to the overlay engine
// @Summary Submit transaction
// @Description Submit a transaction to the overlay engine for processing
// @Tags overlay
// @Accept application/octet-stream
// @Produce json
// @Param x-topics header []string true "Topic names to submit to"
// @Param transaction body []byte true "Serialized transaction data"
// @Success 200 {object} map[string]interface{} "STEAK response with admission results"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /overlay/submit [post]
func submitTransaction() {}

// lookupQuestion performs a lookup query
// @Summary Lookup query
// @Description Query a lookup service for outputs matching the query
// @Tags overlay
// @Accept json
// @Produce json
// @Param query body object true "Lookup query with service name and query params"
// @Success 200 {object} map[string]interface{} "Lookup response with outputs"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /overlay/lookup [post]
func lookupQuestion() {}

// getTopicManagerDocumentation gets documentation for a topic manager
// @Summary Get topic manager documentation
// @Description Returns markdown documentation for a specific topic manager
// @Tags overlay
// @Produce json
// @Param topicManager query string true "Name of the topic manager"
// @Success 200 {object} map[string]string "documentation field with markdown content"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /overlay/getDocumentationForTopicManager [get]
func getTopicManagerDocumentation() {}

// getLookupServiceProviderDocumentation gets documentation for a lookup service
// @Summary Get lookup service documentation
// @Description Returns markdown documentation for a specific lookup service
// @Tags overlay
// @Produce json
// @Param lookupService query string true "Name of the lookup service"
// @Success 200 {object} map[string]string "documentation field with markdown content"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /overlay/getDocumentationForLookupServiceProvider [get]
func getLookupServiceProviderDocumentation() {}

// requestSyncResponse requests sync data from a topic
// @Summary Request sync response
// @Description Request synchronization data for a topic (GASP protocol)
// @Tags overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Sync request with version and since parameters"
// @Success 200 {object} map[string]interface{} "Sync response with UTXOList"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /overlay/requestSyncResponse [post]
func requestSyncResponse() {}

// requestForeignGASPNode requests a foreign GASP node
// @Summary Request foreign GASP node
// @Description Request a GASP node from a foreign overlay
// @Tags overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Request with graphID, txID, and outputIndex"
// @Success 200 {object} map[string]interface{} "GASP node data"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /overlay/requestForeignGASPNode [post]
func requestForeignGASPNode() {}

// syncAdvertisements syncs advertisements (admin)
// @Summary Sync advertisements
// @Description Trigger advertisement sync (requires admin bearer token)
// @Tags overlay-admin
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]string "message field with status"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /overlay/admin/syncAdvertisements [post]
func syncAdvertisements() {}

// startGASPSync starts GASP sync (admin)
// @Summary Start GASP sync
// @Description Start GASP synchronization (requires admin bearer token)
// @Tags overlay-admin
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]string "message field with status"
// @Router /overlay/admin/startGASPSync [post]
func startGASPSync() {}

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
