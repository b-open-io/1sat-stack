package node

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	admindocs "github.com/b-open-io/1sat-stack/admin/docs"
	broadcastdocs "github.com/b-open-io/1sat-stack/pkg/broadcast/docs"
	opnsdocs "github.com/b-open-io/1sat-stack/pkg/opns/docs"
	ordlockdocs "github.com/b-open-io/1sat-stack/pkg/ordlock/docs"
	paymaildocs "github.com/b-open-io/1sat-stack/pkg/paymail/docs"
	"github.com/b-open-io/1sat-stack/pkg/registrar"
	"github.com/gofiber/fiber/v2"
)

// TestMergedSpecPaths verifies the embedded fragments merge onto the mount
// prefixes the server actually uses.
func TestMergedSpecPaths(t *testing.T) {
	app := fiber.New()
	reg := registrar.New(app, "/1sat")
	noop := func(fiber.Router) {}
	add := func(capability, prefix string, spec []byte) {
		reg.Add(registrar.Registration{Capability: capability, Spec: spec, Mounts: []registrar.Mount{
			{Prefix: prefix, Register: noop},
		}})
	}
	add("opns", "/opns", opnsdocs.Spec)
	add("paymail", "/bsvalias", paymaildocs.Spec)
	add("arcade", "/tx", broadcastdocs.Spec)
	add("market", "/market", ordlockdocs.Spec)
	add("admin", "/admin", admindocs.Spec)
	reg.Finalize()

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/1sat/api-spec/swagger.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("merged spec invalid: %v", err)
	}

	wantPresent := []string{
		"/1sat/opns/origin/{name}",
		"/1sat/opns/overlay/submit",
		"/1sat/bsvalias/id/{paymail}",
		"/.well-known/bsvalias",
		"/1sat/tx/{txid}",
		"/1sat/market/overlay/submit",
		"/1sat/admin/api/whitelist",
		"/1sat/capabilities",
		"/1sat/health",
	}
	for _, p := range wantPresent {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("merged spec missing %s", p)
		}
	}

	wantAbsent := []string{
		"/1sat/arcade/tx",
		"/arcade/tx",
		"/v1/bsvalias/id/{paymail}",
		"/1sat/bsvalias/.well-known/bsvalias",
	}
	for _, p := range wantAbsent {
		if _, ok := doc.Paths[p]; ok {
			t.Errorf("merged spec should not contain %s", p)
		}
	}
}
