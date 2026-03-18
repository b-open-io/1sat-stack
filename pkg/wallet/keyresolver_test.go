package wallet

import (
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

func TestResolveServerKey_FromEnv(t *testing.T) {
	key, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyHex := hex.EncodeToString(key.Serialize())

	t.Setenv("ONESAT_WALLET_SERVER_PRIVATE_KEY", keyHex)

	got, err := ResolveServerKey("/nonexistent/path/server.key", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got != keyHex {
		t.Errorf("got %q, want %q", got, keyHex)
	}
}

func TestResolveServerKey_FromEnv_Invalid(t *testing.T) {
	t.Setenv("ONESAT_WALLET_SERVER_PRIVATE_KEY", "not-valid-hex")

	_, err := ResolveServerKey("/nonexistent/path/server.key", slog.Default())
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestResolveServerKey_FromFile(t *testing.T) {
	key, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyHex := hex.EncodeToString(key.Serialize())

	t.Setenv("ONESAT_WALLET_SERVER_PRIVATE_KEY", "")

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(keyFile, []byte(keyHex), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveServerKey(keyFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got != keyHex {
		t.Errorf("got %q, want %q", got, keyHex)
	}
}

func TestResolveServerKey_FromFile_Invalid(t *testing.T) {
	t.Setenv("ONESAT_WALLET_SERVER_PRIVATE_KEY", "")

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(keyFile, []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveServerKey(keyFile, slog.Default())
	if err == nil {
		t.Fatal("expected error for invalid key in file")
	}
}

func TestResolveServerKey_Generate(t *testing.T) {
	t.Setenv("ONESAT_WALLET_SERVER_PRIVATE_KEY", "")

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "subdir", "server.key")

	got, err := ResolveServerKey(keyFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	// Verify the returned hex is valid
	if _, err := parseKeyHex(got); err != nil {
		t.Fatalf("generated key is invalid: %v", err)
	}

	// Verify the file was written
	data, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("key file not created: %v", err)
	}
	if string(data) != got {
		t.Errorf("file contents %q != returned key %q", string(data), got)
	}

	// Verify file permissions
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestResolveServerKey_Generate_Idempotent(t *testing.T) {
	t.Setenv("ONESAT_WALLET_SERVER_PRIVATE_KEY", "")

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "server.key")

	first, err := ResolveServerKey(keyFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	second, err := ResolveServerKey(keyFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if first != second {
		t.Error("second call returned a different key")
	}
}
