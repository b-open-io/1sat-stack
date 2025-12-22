package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
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
	if v.GetString("server.base_path") != "/api" {
		t.Errorf("expected server.base_path=/api, got %s", v.GetString("server.base_path"))
	}

	// Verify package defaults are set
	if v.GetString("store.mode") != store.ModeDisabled {
		t.Errorf("expected store.mode=disabled, got %s", v.GetString("store.mode"))
	}
	if v.GetString("pubsub.mode") != pubsub.ModeDisabled {
		t.Errorf("expected pubsub.mode=disabled, got %s", v.GetString("pubsub.mode"))
	}
	if v.GetString("beef.mode") != beef.ModeDisabled {
		t.Errorf("expected beef.mode=disabled, got %s", v.GetString("beef.mode"))
	}
	if v.GetString("txo.mode") != txo.ModeDisabled {
		t.Errorf("expected txo.mode=disabled, got %s", v.GetString("txo.mode"))
	}
	if v.GetString("bsv21.mode") != bsv21.ModeDisabled {
		t.Errorf("expected bsv21.mode=disabled, got %s", v.GetString("bsv21.mode"))
	}
	if v.GetString("overlay.mode") != overlay.ModeDisabled {
		t.Errorf("expected overlay.mode=disabled, got %s", v.GetString("overlay.mode"))
	}
	if v.GetBool("ordfs.enabled") != false {
		t.Errorf("expected ordfs.enabled=false, got %v", v.GetBool("ordfs.enabled"))
	}
}

func TestConfigInitializeDisabled(t *testing.T) {
	// Test with all services disabled
	cfg := &Config{
		Server: ServerConfig{
			Port:     8080,
			Host:     "0.0.0.0",
			BasePath: "/api",
		},
		Store:   store.Config{Mode: store.ModeDisabled},
		PubSub:  pubsub.Config{Mode: pubsub.ModeDisabled},
		Beef:    beef.Config{Mode: beef.ModeDisabled},
		TXO:     txo.Config{Mode: txo.ModeDisabled},
		BSV21:   bsv21.Config{Mode: bsv21.ModeDisabled},
		Overlay: overlay.Config{Mode: overlay.ModeDisabled},
		ORDFS:   ordfs.Config{Enabled: false},
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
			BasePath: "/api",
		},
		Store:  store.Config{Mode: store.ModeDisabled},
		PubSub: pubsub.Config{Mode: pubsub.ModeEmbedded},
		Beef:   beef.Config{Mode: beef.ModeDisabled},
		TXO:    txo.Config{Mode: txo.ModeDisabled},
		BSV21:  bsv21.Config{Mode: bsv21.ModeDisabled},
		ORDFS:  ordfs.Config{Enabled: false},
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
