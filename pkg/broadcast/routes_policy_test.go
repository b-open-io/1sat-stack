package broadcast

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/gofiber/fiber/v2"
)

func TestGetPolicyPassthrough(t *testing.T) {
	arcade := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policy" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"policy":{"miningFee":{"satoshis":100,"bytes":1000}},"timestamp":"t"}`)
	}))
	t.Cleanup(arcade.Close)

	client := arcadeclient.New(arcade.URL, "", arcade.Client(), nil)
	routes := NewRoutes(nil, client, nil)

	app := fiber.New()
	g := app.Group("/tx")
	routes.Register(g)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tx/policy", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body arcadeclient.PolicyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Policy.MiningFee.Satoshis != 100 || body.Policy.MiningFee.Bytes != 1000 {
		t.Fatalf("policy = %+v", body.Policy)
	}
}

func TestGetPolicyDoesNotCaptureAsTxid(t *testing.T) {
	arcade := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policy":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"policy":{"miningFee":{"satoshis":1,"bytes":1000}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(arcade.Close)

	client := arcadeclient.New(arcade.URL, "", arcade.Client(), nil)
	routes := NewRoutes(nil, client, nil)
	app := fiber.New()
	g := app.Group("/tx")
	routes.Register(g)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tx/policy", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, policy should not be treated as txid", resp.StatusCode)
	}
}
