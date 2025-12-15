package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

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

// @BasePath /api

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to config file")
	flag.Parse()

	// Setup logger
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(log)

	// Load configuration
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
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

	// Create Fiber app
	app := fiber.New(fiber.Config{
		AppName:               "1sat-stack",
		DisableStartupMessage: false,
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())

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

	// Graceful shutdown
	if err := app.Shutdown(); err != nil {
		log.Error("server shutdown error", "error", err)
	}

	log.Info("server stopped")
}
