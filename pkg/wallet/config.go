package wallet

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Config holds wallet service configuration.
type Config struct {
	Mode                     string        `mapstructure:"mode"`                      // disabled, embedded, remote
	ServerPrivateKey         string        `mapstructure:"server_private_key"`        // Server private key for BRC-100 auth
	Name                     string        `mapstructure:"name"`                      // Storage name identifier
	FeeModel                 defs.FeeModel `mapstructure:"fee_model"`                // Fee model configuration
	DB                       defs.Database `mapstructure:"db"`                        // Database configuration (embedded mode only)
	PostgresConnectionString string        `mapstructure:"postgres_connection_string"` // Postgres URI, parsed into DB.PostgreSQL fields
	RemoteURL                string        `mapstructure:"remote_url"`                // URL of standalone wallet-server (remote mode only)
}

// SetDefaults sets viper defaults for wallet configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeEmbedded)
	v.SetDefault(p+"name", "1sat-wallet")
	v.SetDefault(p+"server_private_key", "")
	v.SetDefault(p+"fee_model.type", string(defs.SatPerKB))
	v.SetDefault(p+"fee_model.value", 100)
	v.SetDefault(p+"remote_url", "")

	// Database defaults - SQLite by default (embedded mode only)
	v.SetDefault(p+"db.engine", "sqlite")
	v.SetDefault(p+"db.sqlite.connection_string", "wallet.sqlite")
	v.SetDefault(p+"postgres_connection_string", "")
	v.SetDefault(p+"db.max_idle_connections", 5)
	v.SetDefault(p+"db.max_open_connections", 5)
}

// expandPath expands ~ to the user's home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	} else if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	return path
}

// ExpandDBPath expands the ~ in SQLite connection string and parses
// postgres connection URIs into the toolbox's individual fields.
func (c *Config) ExpandDBPath() {
	if c.DB.Engine == defs.DBTypeSQLite {
		c.DB.SQLite.ConnectionString = expandPath(c.DB.SQLite.ConnectionString)
	}
	if c.DB.Engine == defs.DBTypePostgres && c.PostgresConnectionString != "" {
		if parsed, err := url.Parse(c.PostgresConnectionString); err == nil {
			c.DB.PostgreSQL.Host = parsed.Hostname()
			c.DB.PostgreSQL.Port = parsed.Port()
			c.DB.PostgreSQL.User = parsed.User.Username()
			c.DB.PostgreSQL.Password, _ = parsed.User.Password()
			c.DB.PostgreSQL.DBName = strings.TrimPrefix(parsed.Path, "/")
			if sslMode := parsed.Query().Get("sslmode"); sslMode != "" {
				c.DB.PostgreSQL.SslMode = sslMode
			}
			if tz := parsed.Query().Get("TimeZone"); tz != "" {
				c.DB.PostgreSQL.TimeZone = tz
			} else if c.DB.PostgreSQL.TimeZone == "" {
				c.DB.PostgreSQL.TimeZone = "UTC"
			}
		}
	}
}
