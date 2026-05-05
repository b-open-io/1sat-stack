package paymail

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/broadcast"
	"github.com/b-open-io/1sat-stack/pkg/opns"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/spf13/viper"
)

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEnabled  = "enabled"
)

// Config holds paymail service configuration.
type Config struct {
	Mode   string       `mapstructure:"mode"`
	DBPath string       `mapstructure:"db_path"`
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for paymail configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"db_path", "paymail.db")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/bsvalias")
}

// InitializeDeps holds dependencies for paymail initialization.
type InitializeDeps struct {
	OpnsLookup       *opns.LookupService
	Ordfs            *ordfs.Ordfs
	BroadcastHandler *broadcast.Handler
	BeefStorage      *beef.Storage
	MessageBoxClient *MessageBoxClient
}

// Services holds initialized paymail services.
type Services struct {
	Service *Service
	Routes  *Routes
	store   PendingStore
}

// Close releases resources held by paymail services.
func (s *Services) Close() error {
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

// Initialize creates paymail services from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	deps *InitializeDeps,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	if deps.OpnsLookup == nil {
		return nil, fmt.Errorf("OpNS lookup service is required for paymail")
	}
	if deps.Ordfs == nil {
		return nil, fmt.Errorf("ORDFS service is required for paymail")
	}
	if deps.BroadcastHandler == nil {
		return nil, fmt.Errorf("broadcast handler is required for paymail")
	}
	dbPath := expandPath(c.DBPath)
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open paymail store: %w", err)
	}

	service := NewService(deps.OpnsLookup, deps.Ordfs, deps.BroadcastHandler, deps.MessageBoxClient, deps.BeefStorage, store, logger)

	svc := &Services{
		Service: service,
		store:   store,
	}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(service, logger, "")
	}

	logger.Info("paymail service initialized", "mode", c.Mode, "db", dbPath)
	return svc, nil
}
