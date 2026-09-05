package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	configpkg "github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/ecosystemalias"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/wallet"
	"github.com/spf13/viper"
)

func TestConfigSetDefaults(t *testing.T) {
	cfg := &Config{}
	v := viper.New()
	cfg.SetDefaults(v)

	// Verify server defaults
	if v.GetInt("server.port") != 8080 {
		t.Errorf("expected server.port=8080, got %d", v.GetInt("server.port"))
	}
	if v.GetString("server.host") != "0.0.0.0" {
		t.Errorf("expected server.host=0.0.0.0, got %s", v.GetString("server.host"))
	}
	if v.GetString("server.base_path") != "/1sat" {
		t.Errorf("expected server.base_path=/1sat, got %s", v.GetString("server.base_path"))
	}

	// Verify package defaults are set
	if v.GetString("store.mode") != store.ModeEmbedded {
		t.Errorf("expected store.mode=embedded, got %s", v.GetString("store.mode"))
	}
	if v.GetString("pubsub.mode") != pubsub.ModeEmbedded {
		t.Errorf("expected pubsub.mode=embedded, got %s", v.GetString("pubsub.mode"))
	}
	if v.GetString("beef.mode") != beef.ModeEmbedded {
		t.Errorf("expected beef.mode=embedded, got %s", v.GetString("beef.mode"))
	}
	if v.GetString("txo.mode") != txo.ModeEmbedded {
		t.Errorf("expected txo.mode=embedded, got %s", v.GetString("txo.mode"))
	}
	if v.GetString("bsv21.mode") != bsv21.ModeDisabled {
		t.Errorf("expected bsv21.mode=disabled, got %s", v.GetString("bsv21.mode"))
	}
	if v.GetString("overlay.mode") != overlay.ModeDisabled {
		t.Errorf("expected overlay.mode=disabled, got %s", v.GetString("overlay.mode"))
	}
	if v.GetString("ecosystemalias.mode") != ecosystemalias.ModeDisabled {
		t.Errorf("expected ecosystemalias.mode=disabled, got %s", v.GetString("ecosystemalias.mode"))
	}
	if !v.GetBool("ordfs.enabled") {
		t.Errorf("expected ordfs.enabled=true, got %v", v.GetBool("ordfs.enabled"))
	}
}

func TestConfigInitializeDisabled(t *testing.T) {
	// Test with all services disabled
	cfg := &Config{
		Server: ServerConfig{
			Port:     8080,
			Host:     "0.0.0.0",
			BasePath: "/1sat",
		},
		Store:   store.Config{Mode: store.ModeDisabled},
		PubSub:  pubsub.Config{Mode: pubsub.ModeDisabled},
		Beef:    beef.Config{Mode: beef.ModeDisabled},
		TXO:     txo.Config{Mode: txo.ModeDisabled},
		BSV21:   bsv21.Config{Mode: bsv21.ModeDisabled},
		Overlay: overlay.Config{Mode: overlay.ModeDisabled},
		ORDFS:   ordfs.Config{Enabled: false},
		Spends:  spends.Config{Mode: spends.ModeDisabled},
		Wallet:  wallet.Config{},
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	svc, err := cfg.Initialize(ctx, logger)
	if err != nil {
		t.Fatalf("expected no error with all services disabled, got: %v", err)
	}

	// Verify all services are nil when disabled
	if svc.Store != nil {
		t.Error("expected store to be nil when disabled")
	}
	if svc.PubSub != nil {
		t.Error("expected pubsub to be nil when disabled")
	}
	if svc.Beef != nil {
		t.Error("expected beef to be nil when disabled")
	}
	if svc.TXO != nil {
		t.Error("expected txo to be nil when disabled")
	}
	if svc.BSV21 != nil {
		t.Error("expected bsv21 to be nil when disabled")
	}
	if svc.Overlay != nil {
		t.Error("expected overlay to be nil when disabled")
	}
	if svc.ORDFS != nil {
		t.Error("expected ordfs to be nil when disabled")
	}

	// Close should succeed with nil services
	if err := svc.Close(); err != nil {
		t.Fatalf("expected no error closing, got: %v", err)
	}
}

func TestConfigInitializeEmbeddedPubSub(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:     8080,
			Host:     "0.0.0.0",
			BasePath: "/1sat",
		},
		Store:  store.Config{Mode: store.ModeDisabled},
		PubSub: pubsub.Config{Mode: pubsub.ModeEmbedded},
		Beef:   beef.Config{Mode: beef.ModeDisabled},
		TXO:    txo.Config{Mode: txo.ModeDisabled},
		BSV21:  bsv21.Config{Mode: bsv21.ModeDisabled},
		ORDFS:  ordfs.Config{Enabled: false},
		Spends: spends.Config{Mode: spends.ModeDisabled},
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	svc, err := cfg.Initialize(ctx, logger)
	if err != nil {
		t.Fatalf("failed to initialize with embedded pubsub: %v", err)
	}
	defer svc.Close()

	if svc.PubSub == nil {
		t.Error("expected pubsub to be initialized")
	}
	if svc.PubSub.PubSub == nil {
		t.Error("expected pubsub.PubSub to be initialized")
	}
}

func TestLoadConfig(t *testing.T) {
	// Test loading config without a file
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("failed to load config without file: %v", err)
	}

	// Verify defaults are set
	if cfg.Server.Port != 8080 {
		t.Errorf("expected server.port=8080, got %d", cfg.Server.Port)
	}
}

func TestServicesClose(t *testing.T) {
	// Test Close with nil services
	svc := &Services{}
	if err := svc.Close(); err != nil {
		t.Fatalf("expected no error closing nil services, got: %v", err)
	}
}

func TestApplyRuntimeConfigEnablesEcosystemAlias(t *testing.T) {
	cfg := &Config{}
	err := cfg.applyRuntimeConfig(&configpkg.RuntimeConfig{
		SetupComplete:                    true,
		EcosystemAliasEnabled:            true,
		EcosystemAliasEnabledSet:         true,
		EcosystemAliasSyncEnabled:        true,
		EcosystemAliasSyncEnabledSet:     true,
		EcosystemAliasSyncSubID:          "subscription-id",
		EcosystemAliasSyncConcurrency:    12,
		EcosystemAliasSyncConcurrencySet: true,
		EcosystemAliasSyncBatchSize:      750,
		EcosystemAliasSyncBatchSizeSet:   true,
		EcosystemAliasLogLevel:           "debug",
		EcosystemAliasRoutesEnabled:      false,
		EcosystemAliasRoutesEnabledSet:   true,
		EcosystemAliasRoutePrefix:        "/identity",
		EcosystemAliasRoutePrefixSet:     true,
	})
	if err != nil {
		t.Fatalf("applyRuntimeConfig: %v", err)
	}

	if cfg.EcosystemAlias.Mode != ecosystemalias.ModeEmbedded || cfg.Overlay.Mode != overlay.ModeEmbedded {
		t.Fatalf("modes = ecosystemalias:%q overlay:%q", cfg.EcosystemAlias.Mode, cfg.Overlay.Mode)
	}
	if cfg.EcosystemAlias.Sync == nil || !cfg.EcosystemAlias.Sync.Enabled ||
		cfg.EcosystemAlias.Sync.SubscriptionID != "subscription-id" ||
		cfg.EcosystemAlias.Sync.Concurrency != 12 || cfg.EcosystemAlias.Sync.BatchSize != 750 {
		t.Fatalf("sync config = %+v", cfg.EcosystemAlias.Sync)
	}
	if cfg.EcosystemAlias.LogLevel != "debug" {
		t.Fatalf("log level = %q, want debug", cfg.EcosystemAlias.LogLevel)
	}
	if cfg.EcosystemAlias.Routes.Enabled || cfg.EcosystemAlias.Routes.Prefix != "/identity" {
		t.Fatalf("routes = %+v, want disabled /identity", cfg.EcosystemAlias.Routes)
	}
}

func TestApplyRuntimeConfigCanDisableEcosystemAliasControls(t *testing.T) {
	cfg := &Config{
		EcosystemAlias: ecosystemalias.Config{
			Mode:   ecosystemalias.ModeEmbedded,
			Sync:   &overlay.OverlaySyncConfig{Enabled: true},
			Routes: ecosystemalias.RoutesConfig{Enabled: true, Prefix: "/ecosystemalias"},
		},
	}
	err := cfg.applyRuntimeConfig(&configpkg.RuntimeConfig{
		SetupComplete:                  true,
		EcosystemAliasEnabledSet:       true,
		EcosystemAliasEnabled:          false,
		EcosystemAliasSyncEnabledSet:   true,
		EcosystemAliasSyncEnabled:      false,
		EcosystemAliasRoutesEnabledSet: true,
		EcosystemAliasRoutesEnabled:    false,
	})
	if err != nil {
		t.Fatalf("applyRuntimeConfig: %v", err)
	}

	if cfg.EcosystemAlias.Mode != ecosystemalias.ModeDisabled {
		t.Fatalf("mode = %q, want disabled", cfg.EcosystemAlias.Mode)
	}
	if cfg.EcosystemAlias.Sync == nil || cfg.EcosystemAlias.Sync.Enabled {
		t.Fatalf("sync = %+v, want disabled", cfg.EcosystemAlias.Sync)
	}
	if cfg.EcosystemAlias.Routes.Enabled {
		t.Fatal("routes enabled, want disabled")
	}
}

func TestApplyRuntimeConfigRejectsInvalidEcosystemAliasSettings(t *testing.T) {
	for _, tc := range []struct {
		name string
		rc   configpkg.RuntimeConfig
	}{
		{name: "zero concurrency", rc: configpkg.RuntimeConfig{EcosystemAliasSyncConcurrencySet: true}},
		{name: "oversized batch", rc: configpkg.RuntimeConfig{EcosystemAliasSyncBatchSize: configpkg.EcosystemAliasMaxBatchSize + 1, EcosystemAliasSyncBatchSizeSet: true}},
		{name: "root route", rc: configpkg.RuntimeConfig{EcosystemAliasRoutePrefix: "/", EcosystemAliasRoutePrefixSet: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			tc.rc.SetupComplete = true
			if err := cfg.applyRuntimeConfig(&tc.rc); err == nil {
				t.Fatal("applyRuntimeConfig accepted invalid ecosystem-alias setting")
			}
		})
	}
}

func TestApplyRuntimeConfigClearsEcosystemAliasSubscription(t *testing.T) {
	cfg := &Config{EcosystemAlias: ecosystemalias.Config{Sync: &overlay.OverlaySyncConfig{Enabled: true, SubscriptionID: "old-subscription"}}}
	if err := cfg.applyRuntimeConfig(&configpkg.RuntimeConfig{SetupComplete: true, EcosystemAliasSyncSubIDSet: true}); err != nil {
		t.Fatal(err)
	}
	if cfg.EcosystemAlias.Sync.SubscriptionID != "" {
		t.Fatal("saved empty subscription did not override static configuration")
	}
	if !cfg.EcosystemAlias.Sync.Enabled {
		t.Fatal("clearing subscription disabled queue worker")
	}
}
