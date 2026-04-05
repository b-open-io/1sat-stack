package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/b-open-io/1sat-stack/pkg/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/monitor"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	toolboxwallet "github.com/bsv-blockchain/go-wallet-toolbox/pkg/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

func main() {
	configFile := "wallet-server-config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	cfg, err := loadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.Logging.Level),
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	network := defs.NetworkMainnet
	if cfg.BSVNetwork == "test" {
		network = defs.NetworkTestnet
	}

	// HTTPWalletServices calls back to 1sat-stack for chain/broadcast/BEEF
	httpServices := wallet.NewHTTPWalletServices(cfg.OnesatURL, logger)

	storageIdentityKey, err := wdk.IdentityKey(cfg.ServerPrivateKey)
	if err != nil {
		logger.Error("failed to derive identity key", "error", err)
		os.Exit(1)
	}

	provider, err := storage.NewGORMProvider(network, httpServices,
		storage.WithDBConfig(cfg.DB),
		storage.WithFeeModel(cfg.FeeModel),
		storage.WithLogger(logger),
		storage.WithBackgroundBroadcasterContext(ctx),
	)
	if err != nil {
		logger.Error("failed to create storage provider", "error", err)
		os.Exit(1)
	}

	if _, err := provider.Migrate(ctx, cfg.Name, storageIdentityKey); err != nil {
		logger.Error("failed to migrate storage", "error", err)
		os.Exit(1)
	}

	serverWallet, err := toolboxwallet.New(
		network,
		cfg.ServerPrivateKey,
		provider,
		toolboxwallet.WithLogger(logger),
		toolboxwallet.WithServices(httpServices),
	)
	if err != nil {
		logger.Error("failed to create server wallet", "error", err)
		os.Exit(1)
	}

	// Monitor daemon for proof checking, abandoned tx cleanup
	daemon, err := monitor.NewDaemonWithGORMLocker(ctx, logger, provider, provider.Database.DB)
	if err != nil {
		logger.Warn("failed to create monitor daemon, continuing without it", "error", err)
	} else {
		monitorCfg := cfg.Monitor
		if err := daemon.Start(ctx, monitorCfg.Tasks.EnabledTasks()); err != nil {
			logger.Warn("failed to start monitor daemon", "error", err)
		} else {
			logger.Info("monitor daemon started")
		}
	}

	// Storage server with built-in Authrite auth + CORS
	storageServer := storage.NewServer(logger, provider, serverWallet, storage.ServerOptions{
		Port:     cfg.HTTP.Port,
		Monetize: false,
		CalculateRequestPrice: func(_ *http.Request) (int, error) {
			return 0, nil
		},
	})

	logger.Info("starting wallet server",
		"port", cfg.HTTP.Port,
		"network", network,
		"onesat_url", cfg.OnesatURL,
		"identity_key", storageIdentityKey[:16]+"...",
	)

	// Start server in goroutine, wait for signal
	errCh := make(chan error, 1)
	go func() {
		errCh <- storageServer.Start()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("shutting down", "signal", sig)
	case err := <-errCh:
		logger.Error("server error", "error", err)
	}

	if daemon != nil {
		_ = daemon.Stop()
	}
	provider.Stop()
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
