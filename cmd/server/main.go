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
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"

	_ "github.com/b-open-io/1sat-stack/docs"
)

// @title 1Sat Stack API
// @version 1.0
// @description Composable BSV blockchain services API
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url https://github.com/b-open-io/1sat-stack

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @BasePath /1sat

func main() {
	// Load .env file if present (optional, won't error if missing)
	_ = godotenv.Load()

	// Parse command line flags
	configPath := flag.String("config", "", "Path to config file")
	logLevel := flag.String("log-level", "", "Log level override (debug, info, warn, error)")
	flag.Parse()

	// Load configuration first (with basic logger for errors)
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Create logger from config, with command-line override if provided
	log := cfg.CreateLogger(*logLevel)
	slog.SetDefault(log)

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
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
		AllowMethods:     "GET,POST,OPTIONS",
		AllowHeaders:     "Content-Type,Authorization,X-CallbackUrl,X-CallbackToken",
		AllowCredentials: false, // Cannot use credentials with wildcard origin
		Next: func(c *fiber.Ctx) bool {
			// Skip Fiber CORS for wallet routes - go-wallet-toolbox has its own CORS middleware
			path := c.Path()
			return path == "/.well-known/auth" || path == "/1sat/wallet" || path == "/1sat/wallet/"
		},
	}))

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
