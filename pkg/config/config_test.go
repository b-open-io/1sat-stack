package config

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSetGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "bar" {
		t.Errorf("Get = %q, want %q", got, "bar")
	}
}

func TestGetMissingKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing key: got %v, want ErrNotFound", err)
	}
}

func TestSetOverwrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "key", "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set(ctx, "key", "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, err := s.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v2" {
		t.Errorf("Get after overwrite = %q, want %q", got, "v2")
	}
}

func TestDeleteExisting(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "key", "val"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete(ctx, "key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := s.Get(ctx, "key")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: got %v, want ErrNotFound", err)
	}
}

func TestDeleteNonExisting(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Delete(ctx, "nonexistent"); err != nil {
		t.Errorf("Delete non-existing key: %v", err)
	}
}

func TestListWithPrefix(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entries := map[string]string{
		"overlay.bsv21": "enabled",
		"overlay.bap":   "disabled",
		"server.port":   "8080",
	}
	for k, v := range entries {
		if err := s.Set(ctx, k, v); err != nil {
			t.Fatalf("Set %q: %v", k, err)
		}
	}

	got, err := s.List(ctx, "overlay.")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List overlay. count = %d, want 2", len(got))
	}
	if got["overlay.bsv21"] != "enabled" {
		t.Errorf("overlay.bsv21 = %q, want %q", got["overlay.bsv21"], "enabled")
	}
	if got["overlay.bap"] != "disabled" {
		t.Errorf("overlay.bap = %q, want %q", got["overlay.bap"], "disabled")
	}
}

func TestListEmptyPrefix(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entries := map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
	}
	for k, v := range entries {
		if err := s.Set(ctx, k, v); err != nil {
			t.Fatalf("Set %q: %v", k, err)
		}
	}

	got, err := s.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List all count = %d, want 3", len(got))
	}
}

func TestIsFirstRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.IsFirstRun(ctx)
	if err != nil {
		t.Fatalf("IsFirstRun: %v", err)
	}
	if !first {
		t.Error("IsFirstRun on empty store = false, want true")
	}

	if err := s.Set(ctx, "initialized", "true"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	first, err = s.IsFirstRun(ctx)
	if err != nil {
		t.Fatalf("IsFirstRun after Set: %v", err)
	}
	if first {
		t.Error("IsFirstRun after Set = true, want false")
	}
}

func TestClose(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestLoadRuntimeConfigEcosystemAlias(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	values := map[string]string{
		"overlay.ecosystemalias.enabled":        "true",
		"overlay.ecosystemalias.sync_enabled":   "true",
		"overlay.ecosystemalias.sub_id":         "subscription-id",
		"overlay.ecosystemalias.concurrency":    "12",
		"overlay.ecosystemalias.batch_size":     "750",
		"overlay.ecosystemalias.log_level":      "debug",
		"overlay.ecosystemalias.routes_enabled": "false",
		"overlay.ecosystemalias.route_prefix":   "/identity",
	}
	for key, value := range values {
		if err := s.Set(ctx, key, value); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
	}

	rc, err := LoadRuntimeConfig(ctx, s, slog.Default())
	if err != nil {
		t.Fatalf("LoadRuntimeConfig: %v", err)
	}
	if !rc.EcosystemAliasEnabledSet || !rc.EcosystemAliasEnabled ||
		!rc.EcosystemAliasSyncEnabledSet || !rc.EcosystemAliasSyncEnabled ||
		rc.EcosystemAliasSyncSubID != "subscription-id" ||
		!rc.EcosystemAliasSyncConcurrencySet || rc.EcosystemAliasSyncConcurrency != 12 ||
		!rc.EcosystemAliasSyncBatchSizeSet || rc.EcosystemAliasSyncBatchSize != 750 ||
		rc.EcosystemAliasLogLevel != "debug" || !rc.EcosystemAliasRoutesEnabledSet ||
		rc.EcosystemAliasRoutesEnabled || !rc.EcosystemAliasRoutePrefixSet || rc.EcosystemAliasRoutePrefix != "/identity" {
		t.Fatalf("ecosystem-alias runtime config = %+v", rc)
	}
}

func TestLoadRuntimeConfigRejectsInvalidEcosystemAliasSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid boolean", key: "overlay.ecosystemalias.enabled", value: "yes"},
		{name: "invalid concurrency", key: "overlay.ecosystemalias.concurrency", value: "not-a-number"},
		{name: "zero concurrency", key: "overlay.ecosystemalias.concurrency", value: "0"},
		{name: "excessive concurrency", key: "overlay.ecosystemalias.concurrency", value: "65"},
		{name: "zero batch", key: "overlay.ecosystemalias.batch_size", value: "0"},
		{name: "excessive batch", key: "overlay.ecosystemalias.batch_size", value: "10001"},
		{name: "root route", key: "overlay.ecosystemalias.route_prefix", value: "/"},
		{name: "query route", key: "overlay.ecosystemalias.route_prefix", value: "/identity?x=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if err := s.Set(context.Background(), tc.key, tc.value); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if _, err := LoadRuntimeConfig(context.Background(), s, slog.Default()); err == nil {
				t.Fatal("LoadRuntimeConfig accepted invalid setting")
			}
		})
	}
}

func TestLoadRuntimeConfigNormalizesEcosystemAliasRoutePrefix(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set(context.Background(), "overlay.ecosystemalias.route_prefix", "/identity/"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	rc, err := LoadRuntimeConfig(context.Background(), s, slog.Default())
	if err != nil {
		t.Fatalf("LoadRuntimeConfig: %v", err)
	}
	if !rc.EcosystemAliasRoutePrefixSet || rc.EcosystemAliasRoutePrefix != "/identity" {
		t.Fatalf("route prefix = %q, set=%v", rc.EcosystemAliasRoutePrefix, rc.EcosystemAliasRoutePrefixSet)
	}
}

func TestLoadRuntimeConfigDistinguishesFalseFromUnset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.Set(ctx, "overlay.ecosystemalias.enabled", "false"); err != nil {
		t.Fatalf("Set enabled: %v", err)
	}
	if err := s.Set(ctx, "overlay.ecosystemalias.sync_enabled", "false"); err != nil {
		t.Fatalf("Set sync enabled: %v", err)
	}
	if err := s.Set(ctx, "overlay.ecosystemalias.routes_enabled", "false"); err != nil {
		t.Fatalf("Set routes enabled: %v", err)
	}

	rc, err := LoadRuntimeConfig(ctx, s, slog.Default())
	if err != nil {
		t.Fatalf("LoadRuntimeConfig: %v", err)
	}
	if !rc.EcosystemAliasEnabledSet || rc.EcosystemAliasEnabled ||
		!rc.EcosystemAliasSyncEnabledSet || rc.EcosystemAliasSyncEnabled ||
		!rc.EcosystemAliasRoutesEnabledSet || rc.EcosystemAliasRoutesEnabled {
		t.Fatalf("false settings lost presence: %+v", rc)
	}
}

func TestLoadRuntimeConfigPreservesExplicitEmptySubscription(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set(context.Background(), "overlay.ecosystemalias.sub_id", ""); err != nil {
		t.Fatal(err)
	}
	rc, err := LoadRuntimeConfig(context.Background(), s, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !rc.EcosystemAliasSyncSubIDSet || rc.EcosystemAliasSyncSubID != "" {
		t.Fatal("empty subscription lost presence")
	}
}
