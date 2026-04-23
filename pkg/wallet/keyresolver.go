package wallet

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

// expandPath expands ~ to the user's home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	} else if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	return path
}

// ResolveServerKey resolves the server private key from environment, filesystem, or generates a new one.
// Priority: env var > file > generate.
// If generated, the key is persisted to keyPath for future use.
// Returns the private key as a hex string.
func ResolveServerKey(keyPath string, logger *slog.Logger) (string, error) {
	// 1. Check environment variable
	if keyHex := os.Getenv("ONESAT_WALLET_SERVER_PRIVATE_KEY"); keyHex != "" {
		if _, err := parseKeyHex(keyHex); err != nil {
			return "", fmt.Errorf("invalid key in ONESAT_WALLET_SERVER_PRIVATE_KEY: %w", err)
		}
		logger.Info("server key loaded from environment variable")
		return keyHex, nil
	}

	// 2. Check key file
	expanded := expandPath(keyPath)
	data, err := os.ReadFile(expanded)
	if err == nil {
		keyHex := strings.TrimSpace(string(data))
		if _, err := parseKeyHex(keyHex); err != nil {
			return "", fmt.Errorf("invalid key in %s: %w", expanded, err)
		}
		logger.Info("server key loaded from file", "path", expanded)
		return keyHex, nil
	}

	// 3. Generate new key
	privKey, err := ec.NewPrivateKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate private key: %w", err)
	}
	keyHex := hex.EncodeToString(privKey.Serialize())

	// Ensure parent directory exists
	dir := filepath.Dir(expanded)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(expanded, []byte(keyHex), 0600); err != nil {
		return "", fmt.Errorf("failed to write key file %s: %w", expanded, err)
	}

	logger.Info("server key generated and saved", "path", expanded)
	return keyHex, nil
}

func parseKeyHex(keyHex string) (*ec.PrivateKey, error) {
	bytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	if len(bytes) != 32 {
		return nil, fmt.Errorf("invalid key length: got %d bytes, want 32", len(bytes))
	}
	key, _ := ec.PrivateKeyFromBytes(bytes)
	return key, nil
}
