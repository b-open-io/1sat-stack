package registrar

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestMergedSpec(t *testing.T) {
	opnsFrag := []byte(`{
		"swagger": "2.0",
		"tags": [{"name": "opns"}],
		"paths": {
			"/origin/{name}": {"get": {"tags": ["opns"]}},
			"/overlay/submit": {"post": {"tags": ["opns-overlay"]}}
		},
		"definitions": {"Outpoint": {"type": "string"}}
	}`)
	paymailFrag := []byte(`{
		"swagger": "2.0",
		"tags": [{"name": "paymail"}, {"name": "opns"}],
		"paths": {
			"/id/{paymail}": {"get": {"tags": ["paymail"]}},
			"/.well-known/bsvalias": {"get": {"tags": ["paymail"]}}
		},
		"definitions": {"Outpoint": {"type": "string"}}
	}`)

	tests := []struct {
		name  string
		build func(r *Registrar)
		check func(t *testing.T, doc swaggerFragment)
	}{
		{
			name: "paths rebase onto mount prefix",
			build: func(r *Registrar) {
				r.Add(Registration{Capability: "opns", Spec: opnsFrag, Mounts: []Mount{
					{Prefix: "/opns", Register: func(fiber.Router) {}},
				}})
			},
			check: func(t *testing.T, doc swaggerFragment) {
				if _, ok := doc.Paths["/1sat/opns/origin/{name}"]; !ok {
					t.Errorf("origin path not rebased: %v", pathKeys(doc))
				}
				if _, ok := doc.Paths["/1sat/opns/overlay/submit"]; !ok {
					t.Errorf("overlay path not rebased: %v", pathKeys(doc))
				}
			},
		},
		{
			name: "root-anchored paths are not rebased",
			build: func(r *Registrar) {
				r.Add(Registration{Capability: "paymail", Spec: paymailFrag, Mounts: []Mount{
					{Prefix: "/bsvalias", Register: func(fiber.Router) {}},
				}})
			},
			check: func(t *testing.T, doc swaggerFragment) {
				if _, ok := doc.Paths["/.well-known/bsvalias"]; !ok {
					t.Errorf("well-known path was rebased: %v", pathKeys(doc))
				}
				if _, ok := doc.Paths["/1sat/bsvalias/id/{paymail}"]; !ok {
					t.Errorf("id path not rebased: %v", pathKeys(doc))
				}
			},
		},
		{
			name: "definitions and tags dedupe across fragments",
			build: func(r *Registrar) {
				r.Add(Registration{Capability: "opns", Spec: opnsFrag, Mounts: []Mount{
					{Prefix: "/opns", Register: func(fiber.Router) {}},
				}})
				r.Add(Registration{Capability: "paymail", Spec: paymailFrag, Mounts: []Mount{
					{Prefix: "/bsvalias", Register: func(fiber.Router) {}},
				}})
			},
			check: func(t *testing.T, doc swaggerFragment) {
				if len(doc.Definitions) != 1 {
					t.Errorf("definitions = %d, want 1 (deduped)", len(doc.Definitions))
				}
				if len(doc.Tags) != 2 {
					t.Errorf("tags = %d, want 2 (opns, paymail deduped)", len(doc.Tags))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := fiber.New()
			r := New(app, "/1sat")
			tt.build(r)
			raw, err := r.mergedSpec()
			if err != nil {
				t.Fatalf("mergedSpec: %v", err)
			}
			var doc swaggerFragment
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("merged spec is not valid JSON: %v", err)
			}
			if doc.Swagger != "2.0" || doc.BasePath != "/" {
				t.Errorf("swagger=%q basePath=%q, want 2.0 /", doc.Swagger, doc.BasePath)
			}
			tt.check(t, doc)
		})
	}
}

func TestDocsRoutes(t *testing.T) {
	app := fiber.New()
	r := New(app, "/1sat")
	r.SetDocInfo(DocInfo{Title: "test", Version: "0.0.1"})
	r.Add(Registration{Capability: "opns", Spec: []byte(`{"paths":{"/x":{"get":{}}}}`), Mounts: []Mount{
		{Prefix: "/opns", Register: func(fiber.Router) {}},
	}})
	r.Finalize()

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/1sat/api-spec/swagger.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("spec route status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var doc swaggerFragment
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("served spec invalid: %v", err)
	}
	if doc.Info == nil || doc.Info.Title != "test" {
		t.Errorf("info not applied: %+v", doc.Info)
	}

	page, err := app.Test(httptest.NewRequest(http.MethodGet, "/1sat/docs", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = page.Body.Close() }()
	if page.StatusCode != 200 {
		t.Errorf("docs page status %d", page.StatusCode)
	}
}

func pathKeys(doc swaggerFragment) []string {
	keys := make([]string, 0, len(doc.Paths))
	for k := range doc.Paths {
		keys = append(keys, k)
	}
	return keys
}
