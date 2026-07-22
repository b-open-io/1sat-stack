package node

import (
	"log/slog"
	"net/http"
	"time"

	admindocs "github.com/b-open-io/1sat-stack/admin/docs"
	"github.com/b-open-io/1sat-stack/pkg/auth"
	bapdocs "github.com/b-open-io/1sat-stack/pkg/bap/docs"
	beefdocs "github.com/b-open-io/1sat-stack/pkg/beef/docs"
	broadcastdocs "github.com/b-open-io/1sat-stack/pkg/broadcast/docs"
	bsocialdocs "github.com/b-open-io/1sat-stack/pkg/bsocial/docs"
	bsv21docs "github.com/b-open-io/1sat-stack/pkg/bsv21/docs"
	chaintracksdocs "github.com/b-open-io/1sat-stack/pkg/chaintracks/docs"
	"github.com/b-open-io/1sat-stack/pkg/gateway"
	"github.com/b-open-io/1sat-stack/pkg/httputil"
	opnsdocs "github.com/b-open-io/1sat-stack/pkg/opns/docs"
	ordfsdocs "github.com/b-open-io/1sat-stack/pkg/ordfs/docs"
	ordlockdocs "github.com/b-open-io/1sat-stack/pkg/ordlock/docs"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	ownerdocs "github.com/b-open-io/1sat-stack/pkg/owner/docs"
	paymaildocs "github.com/b-open-io/1sat-stack/pkg/paymail/docs"
	pubsubdocs "github.com/b-open-io/1sat-stack/pkg/pubsub/docs"
	"github.com/b-open-io/1sat-stack/pkg/registrar"
	txodocs "github.com/b-open-io/1sat-stack/pkg/txo/docs"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
)

var (
	Version   = "dev"
	startTime = time.Now()
)

// RegisterRoutes registers all HTTP routes on the Fiber app
func (c *Config) RegisterRoutes(app *fiber.App, svc *Services) {
	// Gateway runs alone: mount the reverse proxy for the public surface
	// instead of any local service routes.
	if c.runsService("gateway") {
		gateway.New(c.Server.BasePath, c.Gateway.Backends, slog.Default()).Register(app)
		return
	}

	// Octet-stream body limit for overlay /submit endpoints. Same source as
	// the outer Fiber BodyLimit so the two stay in sync.
	overlayBodyLimit := int64(ParseBodyLimit(c.Server.BodyLimit))

	reg := registrar.New(app, c.Server.BasePath)
	slog.Debug("registering routes", "basePath", c.Server.BasePath)

	// Redirect root to base path
	if c.Server.BasePath != "" && c.Server.BasePath != "/" {
		reg.Add(registrar.Registration{RootMounts: []registrar.Mount{{
			Register: func(r fiber.Router) {
				r.Get("/", func(ctx *fiber.Ctx) error {
					return ctx.Redirect(c.Server.BasePath+"/home/", fiber.StatusMovedPermanently)
				})
			},
		}}})
	}

	if svc.Beef != nil && svc.Beef.Routes != nil {
		reg.Add(registrar.Registration{Capability: "beef", Spec: beefdocs.Spec, Mounts: []registrar.Mount{
			{Prefix: "/beef", Register: svc.Beef.Routes.Register},
		}})
	}

	// Label predates the /sse mount path; kept for SDK compatibility.
	if svc.PubSub != nil && svc.PubSub.Routes != nil {
		reg.Add(registrar.Registration{Capability: "pubsub", Spec: pubsubdocs.Spec, Mounts: []registrar.Mount{
			{Prefix: "/sse", Register: svc.PubSub.Routes.Register},
		}})
	}

	// TXO responses are mutable — spend status changes
	if c.runsService("index") && svc.TXO != nil && svc.TXO.Routes != nil {
		reg.Add(registrar.Registration{Capability: "txo", Spec: txodocs.Spec, Mounts: []registrar.Mount{
			{Prefix: "/txo", Middlewares: noStore(), Register: svc.TXO.Routes.Register},
		}})
	}

	if svc.Own != nil && svc.Own.Routes != nil {
		reg.Add(registrar.Registration{Capability: "owner", Spec: ownerdocs.Spec, Mounts: []registrar.Mount{
			{Prefix: prefixOr(c.Owner.Routes.Prefix, "/owner"), Middlewares: noStore(), Register: svc.Own.Routes.Register},
		}})
	} else {
		slog.Debug("owner routes not registered", "ownNil", svc.Own == nil, "ownMode", c.Owner.Mode)
	}

	if svc.BSV21 != nil {
		reg.Add(registrar.Registration{
			Capability: "bsv21",
			Spec:       bsv21docs.Spec,
			Mounts: moduleMounts(prefixOr(c.BSV21.Routes.Prefix, "/bsv21"), "/bsv21/overlay",
				registerFunc(svc.BSV21.Routes), svc.BSV21.OverlayRoutes, overlayBodyLimit),
		})
	}

	if svc.BAP != nil {
		reg.Add(registrar.Registration{
			Capability: "bap",
			Spec:       bapdocs.Spec,
			Mounts: moduleMounts(prefixOr(c.BAP.Routes.Prefix, "/bap"), "/bap/overlay",
				registerFunc(svc.BAP.Routes), svc.BAP.OverlayRoutes, overlayBodyLimit),
		})
	}

	if svc.BSocial != nil {
		reg.Add(registrar.Registration{
			Capability: "bsocial",
			Spec:       bsocialdocs.Spec,
			Mounts: moduleMounts(prefixOr(c.BSocial.Routes.Prefix, "/bsocial"), "/bsocial/overlay",
				registerFunc(svc.BSocial.Routes), svc.BSocial.OverlayRoutes, overlayBodyLimit),
		})
	}

	if svc.OPNS != nil {
		reg.Add(registrar.Registration{
			Capability: "opns",
			Spec:       opnsdocs.Spec,
			Mounts: moduleMounts(prefixOr(c.OPNS.Routes.Prefix, "/opns"), "/opns/overlay",
				registerFunc(svc.OPNS.Routes), svc.OPNS.OverlayRoutes, overlayBodyLimit),
		})
	}

	if svc.OrdLock != nil {
		reg.Add(registrar.Registration{
			Capability: "market",
			Spec:       ordlockdocs.Spec,
			Mounts: moduleMounts(prefixOr(c.OrdLock.Routes.Prefix, "/market"), "/market/overlay",
				registerFunc(svc.OrdLock.Routes), svc.OrdLock.OverlayRoutes, overlayBodyLimit),
		})
	}

	if svc.Overlay != nil {
		reg.Add(registrar.Registration{Capability: "overlay"})
	}

	if svc.ORDFS != nil && svc.ORDFS.Routes != nil {
		reg.Add(registrar.Registration{
			Capability: "ordfs",
			Spec:       ordfsdocs.Spec,
			Mounts: []registrar.Mount{
				{Prefix: prefixOr(c.ORDFS.Routes.Prefix, "/ordfs"), Register: svc.ORDFS.Routes.Register},
			},
			// Content at root level for compatibility with the ordfs protocol
			RootMounts: []registrar.Mount{
				{Prefix: "/content", Register: svc.ORDFS.Routes.RegisterContent},
			},
		})
	}

	if svc.ChaintracksRoutes != nil {
		reg.Add(registrar.Registration{Capability: "chaintracks", Spec: chaintracksdocs.Spec, Mounts: []registrar.Mount{
			{Prefix: "/chaintracks", Register: svc.ChaintracksRoutes.Register},
		}})
	}

	// Broadcast routes (POST /tx, GET /tx/:txid). Label predates the arcade
	// removal; kept for SDK compatibility.
	if svc.BroadcastRoutes != nil {
		reg.Add(registrar.Registration{Capability: "arcade", Spec: broadcastdocs.Spec, Mounts: []registrar.Mount{
			{Prefix: "/tx", Register: svc.BroadcastRoutes.Register},
		}})
	}

	// /.well-known/auth at app root for BRC-103/104 handshake. The auth
	// middleware intercepts handshake requests before they reach the no-op;
	// a 404 is only returned for non-handshake requests hitting this path.
	if svc.AuthMiddleware != nil {
		reg.Add(registrar.Registration{RootMounts: []registrar.Mount{{
			Register: func(r fiber.Router) {
				noOp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
				r.All("/.well-known/auth", adaptor.HTTPHandler(svc.AuthMiddleware.HTTPHandler(noOp)))
			},
		}}})
	}

	// Admin: static UI files are public, API endpoints are guarded. Setup
	// routes (status, setup) need identity but not AdminGuard. API routes
	// mount under {prefix}/api/ with AdminGuard; static UI mounts at
	// {prefix}/ without auth so the browser can load the app before the
	// BRC-103/104 handshake.
	if svc.Admin != nil && svc.Admin.Routes != nil && svc.AuthMiddleware != nil {
		reg.Add(registrar.Registration{Capability: "admin", Spec: admindocs.Spec, Mounts: []registrar.Mount{{
			Prefix:      prefixOr(c.Admin.Routes.Prefix, "/admin"),
			Middlewares: []fiber.Handler{httputil.PrivateNoStoreMiddleware()},
			Register: func(g fiber.Router) {
				guarded := g.Group("/api",
					svc.AuthMiddleware.Handler(),
					auth.AdminGuard(svc.ConfigStore, c.Auth.AllowUnauthenticated, slog.Default()),
				)
				svc.Admin.Routes.Register(guarded, g, svc.AuthMiddleware.Handler())
			},
		}}})
	}

	if svc.Sweep != nil && svc.Sweep.Routes != nil {
		reg.Add(registrar.Registration{Capability: "sweep", Mounts: []registrar.Mount{
			{Prefix: prefixOr(c.Sweep.Routes.Prefix, "/sweep"), Register: svc.Sweep.Routes.Register},
		}})
	}

	if svc.Paymail != nil && svc.Paymail.Routes != nil {
		prefix := prefixOr(c.Paymail.Routes.Prefix, "/bsvalias")
		svc.Paymail.Routes.SetPathPrefix(c.Server.BasePath + prefix)
		reg.Add(registrar.Registration{
			Capability: "paymail",
			Spec:       paymaildocs.Spec,
			Mounts: []registrar.Mount{
				{Prefix: prefix, Register: svc.Paymail.Routes.Register},
			},
			// /.well-known/bsvalias at app root for capability discovery
			RootMounts: []registrar.Mount{
				{Register: func(fiber.Router) { svc.Paymail.Routes.RegisterWellKnown(app) }},
			},
		})
	}

	reg.Add(registrar.Registration{Mounts: []registrar.Mount{{
		Register: func(r fiber.Router) { r.Get("/health", handleHealth(svc.Chaintracks)) },
	}}})

	reg.SetDocInfo(registrar.DocInfo{
		Title:       "1Sat Stack API",
		Description: "Composable BSV blockchain services API",
		Version:     Version,
	})

	// Landing page (no auth required)
	if svc.Landing != nil && svc.Landing.Routes != nil {
		prefix := prefixOr(c.Landing.Routes.Prefix, "/home")
		reg.Add(registrar.Registration{Mounts: []registrar.Mount{
			{Prefix: prefix, Register: svc.Landing.Routes.Register},
			{Register: func(r fiber.Router) {
				// Redirect base path to landing page
				r.Get("/", func(ctx *fiber.Ctx) error {
					return ctx.Redirect(ctx.Path()+prefix+"/", fiber.StatusTemporaryRedirect)
				})
			}},
		}})
	}

	reg.Finalize()
}

func prefixOr(prefix, fallback string) string {
	if prefix == "" {
		return fallback
	}
	return prefix
}

func noStore() []fiber.Handler {
	return []fiber.Handler{httputil.NoStoreMiddleware()}
}

// registerFunc returns the Register method of routes, or nil when routes is nil.
func registerFunc[T any, PT interface {
	*T
	Register(fiber.Router)
}](routes PT) func(fiber.Router) {
	if routes == nil {
		return nil
	}
	return routes.Register
}

// moduleMounts builds the standard overlay-module mount pair: lookup routes
// at prefix, engine overlay routes at overlayPrefix.
func moduleMounts(prefix, overlayPrefix string, routes func(fiber.Router), overlayRoutes *overlay.Routes, bodyLimit int64) []registrar.Mount {
	mounts := []registrar.Mount{}
	if routes != nil {
		mounts = append(mounts, registrar.Mount{Prefix: prefix, Middlewares: noStore(), Register: routes})
	}
	if overlayRoutes != nil {
		mounts = append(mounts, registrar.Mount{
			Prefix:      overlayPrefix,
			Middlewares: noStore(),
			Register:    func(r fiber.Router) { overlayRoutes.Register(r, bodyLimit) },
		})
	}
	return mounts
}

// handleHealth returns the health status
// @Summary Health check
// @Description Returns health status with version, uptime, and block height
// @Tags system
// @Produce json
// @Success 200 {object} map[string]interface{} "status, version, uptime, height"
// @Router /health [get]
func handleHealth(ct chaintracks.Chaintracks) fiber.Handler {
	return func(c *fiber.Ctx) error {
		resp := fiber.Map{
			"status":  "ok",
			"version": Version,
			"uptime":  int(time.Since(startTime).Seconds()),
		}
		if ct != nil {
			height := ct.GetHeight(c.Context())
			if height > 0 {
				resp["height"] = height
			}
		}
		return c.JSON(resp)
	}
}
