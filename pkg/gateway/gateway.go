package gateway

import (
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
)

// serviceRoute is one path prefix owned by a backend service. The prefixes
// mirror the ones node.RegisterRoutes mounts (moduleMounts, prefixOr defaults,
// RootMounts) so the gateway's public surface matches a single-process node.
type serviceRoute struct {
	service string
	prefix  string
	root    bool // true = app-root mount (not under base path), e.g. /content
}

// routeTable is the path→service map. Overlay modules mount both /{name} and
// /{name}/overlay; the /{name} prefix covers both.
var routeTable = []serviceRoute{
	{service: "index", prefix: "/txo"},
	{service: "index", prefix: "/owner"},
	{service: "index", prefix: "/tx"},
	{service: "index", prefix: "/sse"},
	{service: "beef", prefix: "/beef"},
	{service: "bsv21", prefix: "/bsv21"},
	{service: "opns", prefix: "/opns"},
	{service: "bap", prefix: "/bap"},
	{service: "bsocial", prefix: "/bsocial"},
	{service: "ordlock", prefix: "/market"},
	{service: "ordfs", prefix: "/ordfs"},
	{service: "paymail", prefix: "/bsvalias"},
	{service: "ordfs", prefix: "/content", root: true},
	{service: "paymail", prefix: "/.well-known/bsvalias", root: true},
	{service: "index", prefix: "/.well-known/auth", root: true},
}

// Gateway is a reverse proxy fronting a multi-process deployment.
type Gateway struct {
	basePath string
	backends map[string]string // service → base URL (no trailing slash)
	logger   *slog.Logger
}

// New creates a Gateway. Backend URLs are normalized (trailing slash removed).
func New(basePath string, backends map[string]string, logger *slog.Logger) *Gateway {
	if logger == nil {
		logger = slog.Default()
	}
	norm := make(map[string]string, len(backends))
	for name, url := range backends {
		norm[name] = strings.TrimRight(url, "/")
	}
	return &Gateway{basePath: basePath, backends: norm, logger: logger}
}

// Register mounts the gateway's routes on the fiber app: one proxy route per
// prefix per configured backend, plus the merged capabilities endpoint.
// Prefixes whose backend is not configured are skipped.
func (g *Gateway) Register(app *fiber.App) {
	app.Get(g.basePath+"/capabilities", g.handleCapabilities)

	for _, rt := range routeTable {
		base, ok := g.backends[rt.service]
		if !ok {
			continue
		}
		mountPrefix := rt.prefix
		if !rt.root {
			mountPrefix = g.basePath + rt.prefix
		}
		h := g.proxyHandler(base)
		app.All(mountPrefix, h)
		app.All(mountPrefix+"/*", h)
	}

	g.logger.Info("gateway routes registered", "backends", len(g.backends), "base_path", g.basePath)
}

// proxyHandler forwards the request to base, preserving method, path, query,
// headers, and body.
func (g *Gateway) proxyHandler(base string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		target := base + c.OriginalURL()
		if err := proxy.Do(c, target); err != nil {
			g.logger.Error("gateway proxy failed", "target", target, "error", err)
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "upstream unavailable"})
		}
		return nil
	}
}

func (g *Gateway) handleCapabilities(c *fiber.Ctx) error {
	return c.JSON(g.mergedCapabilities(c.UserContext()))
}
