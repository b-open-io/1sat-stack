package messagebox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/bsv-blockchain/go-messagebox-server/pkg/db"
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

// Config holds message box service configuration.
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

// SetDefaults sets viper defaults for message box configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeEnabled)
	v.SetDefault(p+"db_path", "~/.1sat/messagebox.db")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/messagebox")
}

// Services holds initialized message box services.
type Services struct {
	DB     *db.DB
	Routes *Routes
}

// Close releases resources held by message box services.
func (s *Services) Close() error {
	if s.DB != nil {
		return s.DB.Close()
	}
	return nil
}

// Initialize creates message box services from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	dbPath := expandPath(c.DBPath)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create messagebox db directory: %w", err)
	}

	database, err := db.New("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open messagebox db: %w", err)
	}

	if err := database.Migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate messagebox db: %w", err)
	}

	svc := &Services{
		DB: database,
	}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(database, logger)
	}

	logger.Info("messagebox service initialized", "mode", c.Mode, "db", dbPath)
	return svc, nil
}
