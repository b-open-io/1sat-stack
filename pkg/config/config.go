package config

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a config key does not exist.
var ErrNotFound = errors.New("config key not found")

// Store provides persistent key-value configuration storage.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) (map[string]string, error)
	IsFirstRun(ctx context.Context) (bool, error)
	Close() error
}
