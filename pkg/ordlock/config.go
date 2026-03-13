package ordlock

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	QueueName    = "ordlock"
)

type Config struct {
	Mode        string       `mapstructure:"mode"`
	StoragePath string       `mapstructure:"storage_path"`
	Concurrency int          `mapstructure:"concurrency"`
	Routes      RoutesConfig `mapstructure:"routes"`
}

type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"storage_path", "~/.1sat/ordlock")
	v.SetDefault(p+"concurrency", 8)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/market")
}

type Services struct {
	OrdLock *OrdLock
	Worker  *worker.Worker
	Routes  *Routes
}

func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	beefStorage *beef.Storage,
	kvStore store.Store,
	ps pubsub.PubSub,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		storagePath := c.StoragePath
		if len(storagePath) > 1 && storagePath[:2] == "~/" {
			home, _ := os.UserHomeDir()
			storagePath = filepath.Join(home, storagePath[2:])
		}
		if err := os.MkdirAll(storagePath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create ordlock storage dir: %w", err)
		}

		dbPath := filepath.Join(storagePath, "listings.db")
		db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
		if err != nil {
			return nil, fmt.Errorf("failed to open ordlock database: %w", err)
		}

		ol, err := New(db, beefStorage, logger)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to initialize ordlock: %w", err)
		}

		svc := &Services{OrdLock: ol}

		concurrency := c.Concurrency
		if concurrency <= 0 {
			concurrency = 8
		}
		queueKey := string(txo.KeyQueue(QueueName))

		svc.Worker = worker.New(&worker.Config{
			Store:   kvStore,
			Key:     queueKey,
			Limiter: make(chan struct{}, concurrency),
			Handler: ol.Process,
			Logger:  logger,
		})

		if ps != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   ps,
				Store:    kvStore,
				Patterns: []string{"ordlock", "spend:ordlock"},
				QueueFunc: func(ev pubsub.Event) string {
					return queueKey
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OrdLock event bridge", "error", err)
			}
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(ol, logger)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown ordlock mode: %s", c.Mode)
	}
}

func (s *Services) Start(ctx context.Context) {
	if s.Worker != nil {
		go s.Worker.Start(ctx)
	}
}

func (s *Services) Close() error {
	if s.Worker != nil {
		s.Worker.Stop()
	}
	if s.OrdLock != nil {
		return s.OrdLock.Close()
	}
	return nil
}
