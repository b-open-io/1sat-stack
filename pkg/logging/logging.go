// Package logging provides per-component log level utilities for slog.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ComponentKey is the attribute key used to identify the component in log records.
const ComponentKey = "component"

// Config holds logging configuration
type Config struct {
	// Level is the default log level for all components
	Level string `mapstructure:"level"`
}

// SetDefaults sets default logging configuration
func (c *Config) SetDefaults() {
	if c.Level == "" {
		c.Level = "info"
	}
}

// ParseLevel converts a string to slog.Level
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewLogger creates a new JSON logger with the specified level.
func NewLogger(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: ParseLevel(level),
	}))
}

// NewComponentLogger creates a logger for a specific component with its own log level.
// If levelOverride is empty, it uses the parent logger's level.
// When overriding, each handler in the chain gets its own LeveledHandler wrapper
// so per-component level filtering works correctly with MultiHandler.
func NewComponentLogger(parent *slog.Logger, component string, levelOverride string) *slog.Logger {
	if levelOverride == "" {
		return parent.With(ComponentKey, component)
	}

	level := ParseLevel(levelOverride)
	handler := parent.Handler()

	// If the parent uses a MultiHandler, wrap each inner handler individually
	// so that MultiHandler's per-handler Enabled check sees the overridden level.
	if multi, ok := handler.(*MultiHandler); ok {
		wrapped := make([]slog.Handler, len(multi.handlers))
		for i, h := range multi.handlers {
			wrapped[i] = NewLeveledHandler(h, level)
		}
		handler = &MultiHandler{handlers: wrapped}
	} else {
		handler = NewLeveledHandler(handler, level)
	}

	return slog.New(handler).With(ComponentKey, component)
}

// LogCloser flushes and closes persistent log handlers.
type LogCloser interface {
	Close() error
}

// LoggerResult holds a logger, its closer, and the query-able log store.
type LoggerResult struct {
	Logger   *slog.Logger
	Closer   LogCloser
	LogStore *SQLiteHandler
}

// NewLoggerWithSQLite creates a logger that writes to both stdout and SQLite.
func NewLoggerWithSQLite(level string, dbPath string, opts *SQLiteHandlerOptions) (*LoggerResult, error) {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: ParseLevel(level),
	})

	sqliteHandler, err := NewSQLiteHandler(dbPath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create sqlite log handler: %w", err)
	}

	multi := NewMultiHandler(jsonHandler, sqliteHandler)
	return &LoggerResult{
		Logger:   slog.New(multi),
		Closer:   sqliteHandler,
		LogStore: sqliteHandler,
	}, nil
}

// LeveledHandler wraps an slog.Handler with a specific level filter.
type LeveledHandler struct {
	handler slog.Handler
	level   slog.Level
}

// NewLeveledHandler creates a handler that filters logs below the specified level.
func NewLeveledHandler(handler slog.Handler, level slog.Level) *LeveledHandler {
	return &LeveledHandler{
		handler: handler,
		level:   level,
	}
}

func (h *LeveledHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *LeveledHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.level {
		return nil
	}
	return h.handler.Handle(ctx, r)
}

func (h *LeveledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LeveledHandler{
		handler: h.handler.WithAttrs(attrs),
		level:   h.level,
	}
}

func (h *LeveledHandler) WithGroup(name string) slog.Handler {
	return &LeveledHandler{
		handler: h.handler.WithGroup(name),
		level:   h.level,
	}
}

// BadgerLogger adapts slog.Logger to badger.Logger interface with level filtering.
type BadgerLogger struct {
	Logger *slog.Logger
	Level  slog.Level
}

func (b *BadgerLogger) Errorf(f string, v ...interface{}) {
	b.Logger.Error(fmt.Sprintf(f, v...))
}
func (b *BadgerLogger) Warningf(f string, v ...interface{}) {
	if b.Level <= slog.LevelWarn {
		b.Logger.Warn(fmt.Sprintf(f, v...))
	}
}
func (b *BadgerLogger) Infof(f string, v ...interface{}) {
	if b.Level <= slog.LevelInfo {
		b.Logger.Info(fmt.Sprintf(f, v...))
	}
}
func (b *BadgerLogger) Debugf(f string, v ...interface{}) {
	if b.Level <= slog.LevelDebug {
		b.Logger.Debug(fmt.Sprintf(f, v...))
	}
}
