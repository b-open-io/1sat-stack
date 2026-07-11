package registrar

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRegistrar(t *testing.T) {
	newApp := func() (*fiber.App, *Registrar) {
		app := fiber.New()
		return app, New(app, "/1sat")
	}

	get := func(t *testing.T, app *fiber.App, path string) (int, string) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("request %s failed: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	t.Run("mounts under base path", func(t *testing.T) {
		app, r := newApp()
		r.Add(Registration{Capability: "opns", Mounts: []Mount{{
			Prefix: "/opns",
			Register: func(g fiber.Router) {
				g.Get("/origin/:name", func(c *fiber.Ctx) error {
					return c.SendString(c.Params("name"))
				})
			},
		}}})
		r.Finalize()

		if status, body := get(t, app, "/1sat/opns/origin/alice"); status != 200 || body != "alice" {
			t.Errorf("got %d %q, want 200 alice", status, body)
		}
		if status, _ := get(t, app, "/opns/origin/alice"); status != 404 {
			t.Errorf("bare path should 404, got %d", status)
		}
	})

	t.Run("root mounts bypass base path", func(t *testing.T) {
		app, r := newApp()
		r.Add(Registration{RootMounts: []Mount{{
			Register: func(g fiber.Router) {
				g.Get("/.well-known/test", func(c *fiber.Ctx) error { return c.SendString("ok") })
			},
		}}})
		r.Finalize()

		if status, body := get(t, app, "/.well-known/test"); status != 200 || body != "ok" {
			t.Errorf("got %d %q, want 200 ok", status, body)
		}
	})

	t.Run("capabilities reflect registrations", func(t *testing.T) {
		app, r := newApp()
		r.Add(Registration{Capability: "opns", Mounts: []Mount{{Prefix: "/opns", Register: func(fiber.Router) {}}}})
		r.Add(Registration{Capability: "paymail"})
		r.Add(Registration{Mounts: []Mount{{Register: func(g fiber.Router) {
			g.Get("/health", func(c *fiber.Ctx) error { return c.SendString("ok") })
		}}}})
		r.Finalize()

		status, body := get(t, app, "/1sat/capabilities")
		if status != 200 {
			t.Fatalf("capabilities status %d", status)
		}
		var caps []string
		if err := json.Unmarshal([]byte(body), &caps); err != nil {
			t.Fatalf("invalid capabilities JSON: %v", err)
		}
		want := []string{"opns", "paymail"}
		if len(caps) != len(want) || caps[0] != want[0] || caps[1] != want[1] {
			t.Errorf("capabilities = %v, want %v", caps, want)
		}
		if status, _ := get(t, app, "/1sat/health"); status != 200 {
			t.Errorf("unlabeled mount not reachable")
		}
	})

	t.Run("middlewares apply to mount group", func(t *testing.T) {
		app, r := newApp()
		r.Add(Registration{Capability: "x", Mounts: []Mount{{
			Prefix: "/x",
			Middlewares: []fiber.Handler{func(c *fiber.Ctx) error {
				c.Set("X-Test", "applied")
				return c.Next()
			}},
			Register: func(g fiber.Router) {
				g.Get("/y", func(c *fiber.Ctx) error { return c.SendString("ok") })
			},
		}}})
		r.Finalize()

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/1sat/x/y", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.Header.Get("X-Test") != "applied" {
			t.Error("mount middleware not applied")
		}
	})
}
