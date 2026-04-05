package main

import (
	"fmt"
	"os"

	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/spf13/viper"
)

// Config is the configuration for the standalone wallet server.
type Config struct {
	Name             string        `mapstructure:"name"`
	ServerPrivateKey string        `mapstructure:"server_private_key"`
	BSVNetwork       string        `mapstructure:"bsv_network"`
	OnesatURL        string        `mapstructure:"onesat_url"`
	FeeModel         defs.FeeModel `mapstructure:"fee_model"`
	DB               defs.Database `mapstructure:"db"`
	HTTP             HTTPConfig    `mapstructure:"http"`
	Monitor          defs.Monitor  `mapstructure:"monitor"`
	Logging          LogConfig     `mapstructure:"logging"`
}

type HTTPConfig struct {
	Port uint `mapstructure:"port"`
}

type LogConfig struct {
	Level   string `mapstructure:"level"`
	Handler string `mapstructure:"handler"`
}

func defaultConfig() Config {
	return Config{
		Name:       "1sat-wallet",
		BSVNetwork: "main",
		OnesatURL:  "http://localhost:8080/1sat",
		FeeModel:   defs.DefaultFeeModel(),
		DB:         defs.DefaultDBConfig(),
		HTTP:       HTTPConfig{Port: 8100},
		Monitor:    defs.DefaultMonitorConfig(),
		Logging:    LogConfig{Level: "info", Handler: "json"},
	}
}

func loadConfig(configFile string) (Config, error) {
	cfg := defaultConfig()

	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("WALLET")
	v.AutomaticEnv()

	// Bind common env vars
	_ = v.BindEnv("server_private_key", "SERVER_PRIVATE_KEY")
	_ = v.BindEnv("onesat_url", "ONESAT_URL")

	if configFile != "" {
		v.SetConfigFile(configFile)
		if err := v.ReadInConfig(); err != nil {
			if !os.IsNotExist(err) {
				return cfg, fmt.Errorf("failed to read config file: %w", err)
			}
		}
	}

	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if cfg.ServerPrivateKey == "" {
		return cfg, fmt.Errorf("server_private_key is required")
	}

	if cfg.OnesatURL == "" {
		return cfg, fmt.Errorf("onesat_url is required")
	}

	return cfg, nil
}
