package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	configpkg "github.com/b-open-io/1sat-stack/pkg/config"
	_ "github.com/mattn/go-sqlite3"
)

// A missing store is reported, not created: the reader must stay read-only.
func TestOpenReaderNeverCreatesTheStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")
	if _, err := OpenReader(path); err == nil {
		t.Fatal("expected ErrNoStore")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("reader created the store file")
	}
}

func TestResolveDataDir(t *testing.T) {
	t.Setenv("ONESAT_DATA_DIR", "")
	home, _ := os.UserHomeDir()
	if got := resolveDataDir(""); got != filepath.Join(home, ".1sat") {
		t.Fatalf("default = %s", got)
	}
	if got := resolveDataDir("/tmp/x"); got != "/tmp/x" {
		t.Fatalf("flag = %s", got)
	}
	t.Setenv("ONESAT_DATA_DIR", "/tmp/y")
	if got := resolveDataDir(""); got != "/tmp/y" {
		t.Fatalf("env = %s", got)
	}
}

// The store's listen settings win over the config file, and the admin API
// URL is derived from them with 0.0.0.0 dialled locally.
func TestLoadEnvPrefersStoreAndBuildsAdminURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONESAT_DATA_DIR", dir)
	t.Setenv("ONESAT_SERVER_PORT", "9999")
	ctx := context.Background()

	env, err := LoadEnv(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if env.Port != 9999 || env.AdminAPI() != "http://127.0.0.1:9999/1sat/admin/api" {
		t.Fatalf("env-only: port=%d url=%s", env.Port, env.AdminAPI())
	}

	store, err := configpkg.NewSQLiteStore(filepath.Join(dir, "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "server.port", "8084"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "auth.api_key", "s3cret"); err != nil {
		t.Fatal(err)
	}
	store.Close()

	env, err = LoadEnv(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if env.AdminAPI() != "http://127.0.0.1:8084/1sat/admin/api" {
		t.Fatalf("store override: url=%s", env.AdminAPI())
	}
	r, err := OpenReader(env.ConfigDB)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := r.Get(ctx, "auth.api_key"); err != nil || v != "s3cret" {
		t.Fatalf("read-only get = %q, %v", v, err)
	}
	if _, err := r.Get(ctx, "nope"); err != configpkg.ErrNotFound {
		t.Fatalf("missing key err = %v", err)
	}
	entries, err := r.List(ctx, "server.")
	if err != nil || len(entries) != 1 {
		t.Fatalf("list = %v, %v", entries, err)
	}
}

// With no server listening the write falls back to the store directly.
func TestWriteFallsBackWhenServerDown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONESAT_DATA_DIR", dir)
	t.Setenv("ONESAT_SERVER_PORT", "1") // nothing listens on port 1
	ctx := context.Background()
	store, err := configpkg.NewSQLiteStore(filepath.Join(dir, "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	env, err := LoadEnv(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := write(ctx, env, map[string]string{"overlay.ordlock.concurrency": "1"}, "x"); err != nil {
		t.Fatal(err)
	}
	if err := write(ctx, env, map[string]string{"setup.complete": "true"}, "x"); err == nil {
		t.Fatal("protected key must be refused")
	}
	r, _ := OpenReader(env.ConfigDB)
	defer r.Close()
	if v, _ := r.Get(ctx, "overlay.ordlock.concurrency"); v != "1" {
		t.Fatalf("direct write = %q", v)
	}
	if err := write(ctx, env, map[string]string{"overlay.ordlock.concurrency": ""}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, "overlay.ordlock.concurrency"); err != configpkg.ErrNotFound {
		t.Fatal("unset must delete")
	}
}
