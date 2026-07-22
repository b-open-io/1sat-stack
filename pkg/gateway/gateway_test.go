package gateway_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/gateway"
	"github.com/gofiber/fiber/v2"
)

// newBackend builds an httptest server that serves the given path→body markers
// and a /1sat/capabilities endpoint returning caps.
func newBackend(caps []string, markers map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/1sat/capabilities" {
			_ = json.NewEncoder(w).Encode(caps)
			return
		}
		if body, ok := markers[r.URL.Path]; ok {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestGatewayProxiesToOwningBackend(t *testing.T) {
	index := newBackend([]string{"txo", "owner"}, map[string]string{
		"/1sat/txo/ping": "index-marker",
	})
	defer index.Close()
	opns := newBackend([]string{"opns"}, map[string]string{
		"/1sat/opns/origin/foo": "opns-marker",
	})
	defer opns.Close()

	app := fiber.New()
	gw := gateway.New("/1sat", map[string]string{"index": index.URL, "opns": opns.URL}, slog.Default())
	gw.Register(app)

	cases := []struct {
		path string
		want string
	}{
		{"/1sat/txo/ping", "index-marker"},
		{"/1sat/opns/origin/foo", "opns-marker"},
	}
	for _, tc := range cases {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil), 5000)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(body) != tc.want {
			t.Fatalf("%s: got status=%d body=%q, want %q", tc.path, resp.StatusCode, body, tc.want)
		}
	}
}

func TestGatewayMergesCapabilities(t *testing.T) {
	index := newBackend([]string{"txo", "owner"}, nil)
	defer index.Close()
	opns := newBackend([]string{"opns", "txo"}, nil) // "txo" overlaps → deduped
	defer opns.Close()

	// An unreachable backend must be omitted, not fail the merge. Closing the
	// server immediately makes its URL refuse connections fast.
	dead := newBackend([]string{"beef"}, nil)
	deadURL := dead.URL
	dead.Close()

	app := fiber.New()
	gw := gateway.New("/1sat", map[string]string{
		"index": index.URL,
		"opns":  opns.URL,
		"beef":  deadURL,
	}, slog.Default())
	gw.Register(app)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/1sat/capabilities", nil), 10000)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var caps []string
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	want := []string{"opns", "owner", "txo"} // sorted union, deduped, dead omitted
	if len(caps) != len(want) {
		t.Fatalf("caps = %v, want %v", caps, want)
	}
	for i := range want {
		if caps[i] != want[i] {
			t.Fatalf("caps = %v, want %v", caps, want)
		}
	}
}

func TestGatewaySkipsUnconfiguredPrefixes(t *testing.T) {
	index := newBackend([]string{"txo"}, map[string]string{"/1sat/txo/ping": "ok"})
	defer index.Close()

	app := fiber.New()
	gw := gateway.New("/1sat", map[string]string{"index": index.URL}, slog.Default())
	gw.Register(app)

	// opns is not configured → its prefix is not mounted → 404 from fiber.
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/1sat/opns/origin/foo", nil), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unconfigured prefix: status = %d, want 404", resp.StatusCode)
	}
}
