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
func NewComponentLogger(parent *slog.Logger, component string, levelOverride string) *slog.Logger {
	if levelOverride == "" {
		// Just add the component tag, keep parent's level
		return parent.With(ComponentKey, component)
	}

	// Create a new logger with the specified level and component tag
	level := ParseLevel(levelOverride)
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	return slog.New(handler).With(ComponentKey, component)
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

func (h *LeveledHandler) Enabled(ctx context.Context, level slog.Level) bool {
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

func (b *BadgerLogger) Errorf(format string, args ...any) {
	if b.Level <= slog.LevelError {
		b.Logger.Error(fmt.Sprintf(format, args...))
	}
}

func (b *BadgerLogger) Warningf(format string, args ...any) {
	if b.Level <= slog.LevelWarn {
		b.Logger.Warn(fmt.Sprintf(format, args...))
	}
}

func (b *BadgerLogger) Infof(format string, args ...any) {
	if b.Level <= slog.LevelInfo {
		b.Logger.Info(fmt.Sprintf(format, args...))
	}
}

func (b *BadgerLogger) Debugf(format string, args ...any) {
	if b.Level <= slog.LevelDebug {
		b.Logger.Debug(fmt.Sprintf(format, args...))
	}
}
