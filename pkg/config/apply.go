package config

import (
	"context"
	"fmt"
	"log/slog"
)

// RuntimeConfig holds the runtime settings read from the config store.
// These override static config (Viper) values for module initialization.
type RuntimeConfig struct {
	// Setup
	SetupComplete bool
	AuthMode      string // "local" or "authenticated"

	// Overlay toggles
	BAPEnabled     bool
	OPNSEnabled    bool
	BSV21Enabled   bool
	BSocialEnabled bool
	OrdLockEnabled bool

	// Database overrides (from wizard)
	WalletDBEngine    string // "sqlite" or "postgres"
	WalletSQLitePath  string
	WalletPostgresURL string
	ChaintracksPath   string
	ChaintracksMode   string // "embedded" or "remote"
	ChaintracksURL    string
	ArcadePath        string
	ArcadeMode        string // "embedded" or "remote"
	ArcadeURL         string
	MessageBoxPath    string
}

// LoadRuntimeConfig reads all runtime settings from the config store.
func LoadRuntimeConfig(ctx context.Context, cs Store, logger *slog.Logger) (*RuntimeConfig, error) {
	rc := &RuntimeConfig{}

	rc.SetupComplete = getBool(ctx, cs, "setup.complete")
	rc.AuthMode = getString(ctx, cs, "auth.mode")

	rc.BAPEnabled = getBool(ctx, cs, "overlay.bap.enabled")
	rc.OPNSEnabled = getBool(ctx, cs, "overlay.opns.enabled")
	rc.BSV21Enabled = getBool(ctx, cs, "overlay.bsv21.enabled")
	rc.BSocialEnabled = getBool(ctx, cs, "overlay.bsocial.enabled")
	rc.OrdLockEnabled = getBool(ctx, cs, "overlay.ordlock.enabled")

	rc.WalletDBEngine = getString(ctx, cs, "wallet.db.engine")
	rc.WalletSQLitePath = getString(ctx, cs, "wallet.db.sqlite.path")
	rc.WalletPostgresURL = getString(ctx, cs, "wallet.db.postgres.url")
	rc.ChaintracksPath = getString(ctx, cs, "chaintracks.path")
	rc.ChaintracksMode = getString(ctx, cs, "chaintracks.mode")
	rc.ChaintracksURL = getString(ctx, cs, "chaintracks.url")
	rc.ArcadePath = getString(ctx, cs, "arcade.path")
	rc.ArcadeMode = getString(ctx, cs, "arcade.mode")
	rc.ArcadeURL = getString(ctx, cs, "arcade.url")
	rc.MessageBoxPath = getString(ctx, cs, "messagebox.path")

	if rc.SetupComplete {
		logger.Info("runtime config loaded from config store",
			"authMode", rc.AuthMode,
			"overlays", fmt.Sprintf("bap=%v opns=%v bsv21=%v bsocial=%v ordlock=%v",
				rc.BAPEnabled, rc.OPNSEnabled, rc.BSV21Enabled, rc.BSocialEnabled, rc.OrdLockEnabled),
		)
	} else {
		logger.Info("config store empty — first run, wizard mode")
	}

	return rc, nil
}

func getString(ctx context.Context, cs Store, key string) string {
	val, err := cs.Get(ctx, key)
	if err != nil {
		return ""
	}
	return val
}

func getBool(ctx context.Context, cs Store, key string) bool {
	return getString(ctx, cs, key) == "true"
}
