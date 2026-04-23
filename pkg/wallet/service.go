package wallet

import (
	"context"
	"fmt"
	"log/slog"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

// Services holds the server wallet used for BRC-103/104 signing.
type Services struct {
	Wallet sdk.Interface
}

// Initialize builds a completed proto-wallet from the configured server private key.
// The wallet is used for server-side BRC-103/104 handshakes and any client-side
// authenticated HTTP fetches performed by the server. No storage backend is attached.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if c.ServerPrivateKey == "" {
		return nil, fmt.Errorf("server_private_key is required for wallet service")
	}

	if logger == nil {
		logger = slog.Default()
	}

	privKey, err := ec.PrivateKeyFromHex(c.ServerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server private key: %w", err)
	}

	protoWallet, err := sdk.NewCompletedProtoWallet(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create proto wallet: %w", err)
	}

	logger.Info("wallet service initialized")

	return &Services{Wallet: protoWallet}, nil
}

// Close releases any wallet resources.
func (s *Services) Close() error {
	return nil
}
