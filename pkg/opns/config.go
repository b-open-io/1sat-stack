package opns

import (
	"context"
	"fmt"
	"log/slog"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds OPNS configuration.
type Config struct {
	Mode   string      `mapstructure:"mode"` // disabled, embedded
	Crawl  CrawlConfig `mapstructure:"crawl"`
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for OPNS configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"crawl.enabled", false)
	v.SetDefault(p+"crawl.concurrency", 8)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/opns")
}

// Services holds initialized OPNS services.
type Services struct {
	Lookup       *LookupService
	TopicManager *TopicManager
	Crawl        *GenesisCrawl
	Routes       *Routes
}

// Initialize creates OPNS services from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	db overlaystorage.TopicStorage,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		lookupSvc := NewLookupService(db)

		topicManager := &TopicManager{}

		svc := &Services{
			Lookup:       lookupSvc,
			TopicManager: topicManager,
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(lookupSvc, logger)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown opns mode: %s", c.Mode)
	}
}

// Close closes the OPNS services.
func (s *Services) Close() error {
	if s.Crawl != nil {
		s.Crawl.Stop()
	}
	return nil
}
