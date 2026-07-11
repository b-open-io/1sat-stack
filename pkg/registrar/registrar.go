// Package registrar mounts service routes under a shared base path and
// derives the /capabilities response (and, for services that provide spec
// fragments, the served OpenAPI document) from the same registrations, so
// the advertised surface cannot drift from what is actually mounted.
//
// Note: the landing UI hardcodes the "/1sat" base path in its fetches, so
// compositions serving that UI must use "/1sat".
package registrar

import (
	"github.com/gofiber/fiber/v2"
)

// Mount attaches a service's routes at a prefix. An empty Prefix (with no
// middlewares) registers directly on the parent router instead of a group.
type Mount struct {
	Prefix      string
	Middlewares []fiber.Handler
	Register    func(fiber.Router)
}

// Registration is one service's contribution to the composition: its
// capability label, its mounts under the base path, any app-root mounts
// (/.well-known/*, /content), and an optional OpenAPI fragment.
type Registration struct {
	// Capability is the label advertised by GET {base}/capabilities.
	// Empty means mounted but not advertised (health, docs, landing).
	Capability string
	Mounts     []Mount
	RootMounts []Mount
	// Spec is an OpenAPI 2.0 fragment with service-relative paths; the
	// registrar rebases them onto the mount prefix when serving docs.
	Spec []byte
}

// Registrar accumulates registrations onto a fiber app.
type Registrar struct {
	app          *fiber.App
	basePath     string
	api          fiber.Router
	capabilities []string
	specs        []specEntry
}

type specEntry struct {
	prefix   string // full mount prefix (base path included); "" for root mounts
	fragment []byte
}

// New creates a registrar grouping service mounts under basePath.
func New(app *fiber.App, basePath string) *Registrar {
	return &Registrar{
		app:      app,
		basePath: basePath,
		api:      app.Group(basePath),
	}
}

// BasePath returns the base path all Mounts are grouped under.
func (r *Registrar) BasePath() string {
	return r.basePath
}

// Add mounts a registration's routes and records its capability and spec.
func (r *Registrar) Add(reg Registration) {
	if reg.Capability != "" {
		r.capabilities = append(r.capabilities, reg.Capability)
	}
	for _, m := range reg.Mounts {
		mount(r.api, m)
	}
	for _, m := range reg.RootMounts {
		mount(r.app, m)
	}
	if reg.Spec != nil {
		prefix := ""
		if len(reg.Mounts) > 0 {
			prefix = r.basePath + reg.Mounts[0].Prefix
		}
		r.specs = append(r.specs, specEntry{prefix: prefix, fragment: reg.Spec})
	}
}

// Capabilities returns the labels registered so far.
func (r *Registrar) Capabilities() []string {
	out := make([]string, len(r.capabilities))
	copy(out, r.capabilities)
	return out
}

// Finalize mounts the endpoints derived from the registrations. Call after
// every Add.
func (r *Registrar) Finalize() {
	capabilities := r.Capabilities()
	r.api.Get("/capabilities", func(c *fiber.Ctx) error {
		return c.JSON(capabilities)
	})
}

func mount(parent fiber.Router, m Mount) {
	if m.Register == nil {
		return
	}
	if m.Prefix == "" && len(m.Middlewares) == 0 {
		m.Register(parent)
		return
	}
	m.Register(parent.Group(m.Prefix, m.Middlewares...))
}
