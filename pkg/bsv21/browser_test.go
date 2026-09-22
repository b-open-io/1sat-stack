package bsv21

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestBrowserShell(t *testing.T) {
	app := fiber.New()
	NewRoutes(&RoutesDeps{}).Register(app.Group("/bsv21"))

	t.Run("index redirects to browse", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/bsv21/", nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/bsv21/browse/" {
			t.Fatalf("location %q", loc)
		}
	})

	t.Run("browse shell", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/bsv21/browse/token_0", nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("content-type %q", ct)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "BSV21") {
			t.Fatal("expected browser shell")
		}
	})

	t.Run("tokens stays json", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/bsv21/tokens", nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if strings.TrimSpace(string(body)) != "[]" {
			t.Fatalf("body %s", body)
		}
	})
}
