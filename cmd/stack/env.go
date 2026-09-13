package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	configpkg "github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/spf13/viper"
)

// Env is what the command shares with a running server: the same config
// file and ONESAT_* environment, the same data dir, and therefore the same
// config store. Nothing has to be passed on the command line for the two to
// agree.
type Env struct {
	DataDir     string
	ConfigDB    string
	BasePath    string // server.base_path, e.g. /1sat
	AdminPrefix string // admin.routes.prefix, e.g. /admin
	Host        string // server.host as configured (0.0.0.0 is dialled as 127.0.0.1)
	Port        int
	viper       *viper.Viper
}

// resolveDataDir mirrors cmd/server/main.go: flag, then ONESAT_DATA_DIR, then ~/.1sat.
func resolveDataDir(flagValue string) string {
	dir := flagValue
	if dir == "" {
		dir = os.Getenv("ONESAT_DATA_DIR")
	}
	if dir == "" {
		dir = "~/.1sat"
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	return dir
}

// loadViper mirrors the server's LoadConfig search order and env handling.
// Kept here until `serve` moves under this command, at which point the
// loader in cmd/server/config.go becomes shared code.
func loadViper(configPath string) (*viper.Viper, error) {
	v := viper.New()
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.base_path", "/1sat")
	v.SetDefault("admin.routes.prefix", "/admin")
	v.SetConfigType("yaml")
	v.SetConfigName("config")
	v.SetEnvPrefix("ONESAT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		return v, nil
	}
	v.AddConfigPath(".")
	v.AddConfigPath("$HOME/.1sat")
	v.AddConfigPath("/etc/1sat")
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read config file: %w", err)
		}
	}
	return v, nil
}

// LoadEnv resolves the data dir and config file the way the server does,
// then lets the config store override the listen settings, because the
// running server reads those from the store (pkg/config/apply.go).
func LoadEnv(ctx context.Context, configPath, dataDirFlag string) (*Env, error) {
	v, err := loadViper(configPath)
	if err != nil {
		return nil, err
	}
	e := &Env{
		DataDir:     resolveDataDir(dataDirFlag),
		BasePath:    v.GetString("server.base_path"),
		AdminPrefix: v.GetString("admin.routes.prefix"),
		Host:        v.GetString("server.host"),
		Port:        v.GetInt("server.port"),
		viper:       v,
	}
	e.ConfigDB = filepath.Join(e.DataDir, "config.db")

	if _, statErr := os.Stat(e.ConfigDB); statErr == nil {
		r, err := OpenReader(e.ConfigDB)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		if s, err := r.Get(ctx, "server.base_path"); err == nil && s != "" {
			e.BasePath = s
		}
		if s, err := r.Get(ctx, "server.host"); err == nil && s != "" {
			e.Host = s
		}
		if s, err := r.Get(ctx, "server.port"); err == nil && s != "" {
			if p, perr := strconv.Atoi(s); perr == nil && p > 0 {
				e.Port = p
			}
		}
	}
	return e, nil
}

// AdminAPI is the base URL of the running server's admin API.
func (e *Env) AdminAPI() string {
	host := e.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d%s%s/api", host, e.Port, strings.TrimSuffix(e.BasePath, "/"), strings.TrimSuffix(e.AdminPrefix, "/"))
}

// Reader is a read-only view of the config store. It opens its own
// connection in SQLite read-only mode, which is always safe next to the
// running server (WAL readers never block or get blocked by the writer),
// and it never touches the schema.
type Reader struct{ db *sql.DB }

// ErrNoStore means the config store does not exist yet: the server has never
// been started with this data dir.
var ErrNoStore = errors.New("config store not found")

func OpenReader(path string) (*Reader, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("%w at %s (has the server been started with this data dir?)", ErrNoStore, path)
	}
	// The `file:` form is required for mode=ro to take effect with
	// mattn/go-sqlite3; without it the parameter is ignored and the reader
	// would silently open read-write (and create a missing file).
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open config store: %w", err)
	}
	return &Reader{db: db}, nil
}

func (r *Reader) Close() error { return r.db.Close() }

func (r *Reader) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", configpkg.ErrNotFound
	}
	return value, err
}

func (r *Reader) List(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM config WHERE key LIKE ? ORDER BY key`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
