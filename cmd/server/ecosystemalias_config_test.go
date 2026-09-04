package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/ecosystemalias"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/gofiber/fiber/v2"
)

func TestEcosystemAliasDisabledHasNoCapabilityOrRoutes(t *testing.T) {
	cfg := &Config{Server: ServerConfig{BasePath: "/1sat", BodyLimit: "1mb"}}
	app := fiber.New()
	cfg.RegisterRoutes(app, &Services{})

	capabilities := getCapabilities(t, app)
	if containsString(capabilities, "ecosystemalias") {
		t.Fatalf("disabled capabilities = %v, unexpectedly include ecosystemalias", capabilities)
	}
	assertHTTPStatus(t, app, http.MethodPost, "/1sat/ecosystemalias/overlay/lookup", http.StatusNotFound)
}

func TestEcosystemAliasCustomPrefixOwnsDiscoveryRoutes(t *testing.T) {
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteFactory: %v", err)
	}
	t.Cleanup(func() { _ = factory.Close() })

	moduleCfg := ecosystemalias.Config{
		Mode: ecosystemalias.ModeEmbedded,
		Routes: ecosystemalias.RoutesConfig{
			Enabled: true,
			Prefix:  "/identity",
		},
	}
	moduleSvc, err := moduleCfg.Initialize(t.Context(), nil, &overlay.ModuleDeps{
		Factory:      factory.Factory(),
		TxTopicIndex: factory.TxTopicIndex(),
		BeefStorage:  beef.NewStorageFromProviders(nil, nil),
		RoutesConfig: &overlay.RoutesConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Initialize ecosystem-alias: %v", err)
	}

	cfg := &Config{
		Server:         ServerConfig{BasePath: "/1sat", BodyLimit: "1mb"},
		EcosystemAlias: moduleCfg,
	}
	app := fiber.New()
	cfg.RegisterRoutes(app, &Services{EcosystemAlias: moduleSvc})

	capabilities := getCapabilities(t, app)
	if !containsString(capabilities, "ecosystemalias") {
		t.Fatalf("enabled capabilities = %v, want ecosystemalias", capabilities)
	}

	assertDiscoveryWorks(t, app,
		"/1sat/identity/overlay/getDocumentationForTopicManager?topicManager="+ecosystemalias.TopicName,
		"BRC-169 Ecosystem Alias Topic Manager",
	)
	assertDiscoveryWorks(t, app,
		"/1sat/identity/overlay/getDocumentationForLookupServiceProvider?lookupService="+ecosystemalias.LookupName,
		"BRC-169 ecosystem-alias lookup",
	)
	assertHTTPStatus(t, app, http.MethodGet,
		"/1sat/ecosystemalias/overlay/getDocumentationForTopicManager?topicManager="+ecosystemalias.TopicName,
		http.StatusNotFound,
	)
}

func TestEcosystemAliasRouteDisablementRemovesCapabilityAndSurface(t *testing.T) {
	tests := []struct {
		name         string
		moduleRoutes bool
		globalRoutes bool
	}{
		{name: "module routes disabled", moduleRoutes: false, globalRoutes: true},
		{name: "global overlay routes disabled", moduleRoutes: true, globalRoutes: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
			if err != nil {
				t.Fatalf("NewSQLiteFactory: %v", err)
			}
			t.Cleanup(func() { _ = factory.Close() })

			moduleCfg := ecosystemalias.Config{
				Mode: ecosystemalias.ModeEmbedded,
				Routes: ecosystemalias.RoutesConfig{
					Enabled: tt.moduleRoutes,
					Prefix:  "/ecosystemalias",
				},
			}
			moduleSvc, err := moduleCfg.Initialize(t.Context(), nil, &overlay.ModuleDeps{
				Factory:      factory.Factory(),
				TxTopicIndex: factory.TxTopicIndex(),
				BeefStorage:  beef.NewStorageFromProviders(nil, nil),
				RoutesConfig: &overlay.RoutesConfig{Enabled: tt.globalRoutes},
			})
			if err != nil {
				t.Fatalf("Initialize ecosystem-alias: %v", err)
			}
			if moduleSvc.OverlayRoutes != nil {
				t.Fatal("OverlayRoutes initialized despite disabled route layer")
			}

			cfg := &Config{
				Server:         ServerConfig{BasePath: "/1sat", BodyLimit: "1mb"},
				EcosystemAlias: moduleCfg,
			}
			app := fiber.New()
			cfg.RegisterRoutes(app, &Services{EcosystemAlias: moduleSvc})

			capabilities := getCapabilities(t, app)
			if containsString(capabilities, "ecosystemalias") {
				t.Fatalf("capabilities = %v, unexpectedly include ecosystemalias", capabilities)
			}
			assertHTTPStatus(t, app, http.MethodPost, "/1sat/ecosystemalias/overlay/lookup", http.StatusNotFound)
		})
	}
}

func getCapabilities(t *testing.T, app *fiber.App) []string {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/1sat/capabilities", nil))
	if err != nil {
		t.Fatalf("GET capabilities: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var capabilities []string
	if err := json.NewDecoder(resp.Body).Decode(&capabilities); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	return capabilities
}

func assertDiscoveryWorks(t *testing.T, app *fiber.App, path, want string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
		t.Fatalf("GET %s = %d %s, want 200 containing %q", path, resp.StatusCode, body, want)
	}
}

func assertHTTPStatus(t *testing.T, app *fiber.App, method, path string, want int) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, path, nil))
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s = %d %s, want %d", method, path, resp.StatusCode, body, want)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
