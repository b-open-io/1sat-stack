package messagebox

import (
	"log/slog"
	"net/http"

	"github.com/bsv-blockchain/go-messagebox-server/pkg/db"
	"github.com/bsv-blockchain/go-messagebox-server/pkg/handlers"
)

// Routes provides HTTP handlers for message box endpoints.
type Routes struct {
	server *handlers.Server
	logger *slog.Logger
}

// NewRoutes creates a new Routes instance.
func NewRoutes(database *db.DB, logger *slog.Logger) *Routes {
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		server: &handlers.Server{DB: database},
		logger: logger,
	}
}

// Handler returns a net/http handler with all message box routes registered.
// This is designed to be wrapped with AuthMiddleware.HTTPHandler() and then
// adaptor.HTTPHandler() for Fiber integration — same pattern as wallet routes.
func (r *Routes) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sendMessage", r.server.SendMessage)
	mux.HandleFunc("POST /listMessages", r.server.ListMessages)
	mux.HandleFunc("POST /acknowledgeMessage", r.server.AcknowledgeMessage)
	mux.HandleFunc("POST /registerDevice", r.server.RegisterDevice)
	mux.HandleFunc("GET /devices", r.server.ListDevices)
	mux.HandleFunc("POST /permissions/set", r.server.SetPermission)
	mux.HandleFunc("GET /permissions/get", r.server.GetPermission)
	mux.HandleFunc("GET /permissions/list", r.server.ListPermissions)
	mux.HandleFunc("GET /permissions/quote", r.server.GetQuote)
	return mux
}
