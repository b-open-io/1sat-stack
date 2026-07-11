package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/wallet"

	"github.com/b-open-io/1sat-stack/pkg/registrar"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"
)

var (
	Version   = "dev"
	startTime = time.Now()
	restartCh = make(chan struct{}, 1)
)

// RequestRestart signals the server to re-exec itself.
func RequestRestart() {
	select {
	case restartCh <- struct{}{}:
	default:
	}
}

func main() {
	// Load .env file if present (optional, won't error if missing)
	_ = godotenv.Load()

	// Parse command line flags
	configPath := flag.String("config", "", "Path to config file")
	logLevel := flag.String("log-level", "", "Log level override (debug, info, warn, error)")
	dataDir := flag.String("data-dir", "", "Base directory for all data files (default: ~/.1sat)")
	flag.Parse()

	// Resolve data directory: flag > env > default
	resolvedDataDir := *dataDir
	if resolvedDataDir == "" {
		resolvedDataDir = os.Getenv("ONESAT_DATA_DIR")
	}
	if resolvedDataDir == "" {
		resolvedDataDir = "~/.1sat"
	}
	if home, err := os.UserHomeDir(); err == nil && len(resolvedDataDir) >= 2 && resolvedDataDir[:2] == "~/" {
		resolvedDataDir = filepath.Join(home, resolvedDataDir[2:])
	}

	// Load configuration first (with basic logger for errors)
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	cfg.DataDir = resolvedDataDir

	// Resolve effective log level (CLI override > config)
	effectiveLevel := cfg.Logging.Level
	if *logLevel != "" {
		effectiveLevel = *logLevel
	}
	cfg.Logging.Level = effectiveLevel
	cfg.Logging.SetDefaults()

	// Create logger with persistent SQLite storage
	logsDBPath := filepath.Join(resolvedDataDir, "logs.db")
	logResult, err := logging.NewLoggerWithSQLite(
		effectiveLevel,
		logsDBPath,
		&logging.SQLiteHandlerOptions{
			BatchSize:     100,
			FlushInterval: 1 * time.Second,
			Retention:     7 * 24 * time.Hour,
			PruneInterval: 1 * time.Hour,
			BufferSize:    10000,
		},
	)
	if err != nil {
		slog.Error("failed to create logger", "error", err)
		os.Exit(1)
	}
	defer logResult.Closer.Close()

	log := logResult.Logger
	slog.SetDefault(log)

	// Store LogStore for admin API
	cfg.LogStore = logResult.LogStore

	// Resolve server private key if not yet configured
	if cfg.Wallet.ServerPrivateKey == "" {
		wif, err := wallet.ResolveServerKey(filepath.Join(resolvedDataDir, "server.key"), log)
		if err != nil {
			log.Error("failed to resolve server private key", "error", err)
			os.Exit(1)
		}
		cfg.Wallet.ServerPrivateKey = wif
	}

	// Create context that cancels on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize services
	svc, err := cfg.Initialize(ctx, log)
	if err != nil {
		log.Error("failed to initialize services", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := svc.Close(); err != nil {
			log.Error("failed to close services", "error", err)
		}
	}()

	// Start pprof server if enabled
	if cfg.Server.Pprof.Enabled {
		pprofAddr := fmt.Sprintf("%s:%d", cfg.Server.Pprof.Host, cfg.Server.Pprof.Port)
		go func() {
			log.Info("starting pprof server", "address", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Error("pprof server error", "error", err)
			}
		}()
	}

	// Start JungleBus subscribers in background
	svc.StartSubscribers(ctx, log)

	// Create Fiber app
	bodyLimit := ParseBodyLimit(cfg.Server.BodyLimit)
	log.Info("configuring body limit", "limit", cfg.Server.BodyLimit, "bytes", bodyLimit)
	app := fiber.New(fiber.Config{
		AppName:               "1sat-stack",
		DisableStartupMessage: false,
		BodyLimit:             bodyLimit,
		UnescapePath:          true,
	})

	// Middleware
	app.Use(recover.New())
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		slog.Info("request",
			"status", c.Response().StatusCode(),
			"latency", time.Since(start).String(),
			"ip", c.IP(),
			"method", c.Method(),
			"path", c.Path(),
		)
		return err
	})
	app.Use(registrar.DefaultCORS())

	// Register routes
	cfg.RegisterRoutes(app, svc)

	// Start server in goroutine
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	go func() {
		if err := app.Listen(addr); err != nil {
			log.Error("server error", "error", err)
			cancel()
		}
	}()

	log.Info("server started", "address", addr)

	// Listen for restart requests
	go func() {
		<-restartCh
		log.Info("restarting server...")
		time.Sleep(500 * time.Millisecond)
		executable, err := os.Executable()
		if err != nil {
			log.Error("failed to get executable path, restart aborted", "error", err)
			return
		}
		if err := syscall.Exec(executable, os.Args, os.Environ()); err != nil {
			log.Error("failed to exec, restart aborted", "error", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		log.Info("shutting down server...")
	case <-ctx.Done():
		log.Info("context cancelled, shutting down...")
	}

	// Cancel context first to notify all goroutines
	cancel()

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Error("server shutdown error", "error", err)
	}

	log.Info("server stopped")
}
