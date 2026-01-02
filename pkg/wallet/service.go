package wallet

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/b-open-io/1sat-stack/pkg/logging"
	arcadeconfig "github.com/bsv-blockchain/arcade/config"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/services"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	toolboxwallet "github.com/bsv-blockchain/go-wallet-toolbox/pkg/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

// Services holds initialized wallet services.
type Services struct {
	Provider *storage.Provider
	Server   *storage.Server
	Routes   *Routes
}

// InitializeDeps holds dependencies for wallet service initialization.
type InitializeDeps struct {
	Network string                 // "main" or "test"
	Arcade  *arcadeconfig.Services // Existing Arcade services to share ARC config
}

// Initialize creates a wallet service from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	deps *InitializeDeps,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	if c.ServerPrivateKey == "" {
		return nil, fmt.Errorf("server_private_key is required for wallet service")
	}

	// Create logger with component name
	walletLogger := logging.NewComponentLogger(logger, "wallet", "")

	// Determine network
	network := defs.NetworkMainnet
	if deps.Network == "test" {
		network = defs.NetworkTestnet
	}

	// Create wallet services config - share ARC from Arcade if available
	walletServicesConfig := createWalletServicesConfig(network, deps.Arcade)
	walletServices := services.New(walletLogger, walletServicesConfig)

	// Get storage identity key from server private key
	storageIdentityKey, err := wdk.IdentityKey(c.ServerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage identity key: %w", err)
	}

	// Create GORM storage provider
	providerOptions := []storage.ProviderOption{
		storage.WithDBConfig(c.DB),
		storage.WithLogger(walletLogger),
		storage.WithBackgroundBroadcasterContext(ctx),
	}

	provider, err := storage.NewGORMProvider(network, walletServices, providerOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage provider: %w", err)
	}

	// Run migrations
	name := c.Name
	if name == "" {
		name = "1sat-wallet"
	}
	_, err = provider.Migrate(ctx, name, storageIdentityKey)
	if err != nil {
		return nil, fmt.Errorf("failed to migrate storage: %w", err)
	}

	// Create server wallet from private key
	serverWallet, err := toolboxwallet.New(
		network,
		c.ServerPrivateKey,
		provider,
		toolboxwallet.WithLogger(walletLogger),
		toolboxwallet.WithServices(walletServices),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create server wallet: %w", err)
	}

	// Create storage server
	serverOptions := storage.ServerOptions{
		Port:     0, // Not used when embedding - we use Fiber's port
		Monetize: false,
		CalculateRequestPrice: func(_ *http.Request) (int, error) {
			return 0, nil
		},
	}
	server := storage.NewServer(walletLogger, provider, serverWallet, serverOptions)

	svc := &Services{
		Provider: provider,
		Server:   server,
	}

	// Create routes if enabled
	if c.Routes.Enabled {
		svc.Routes = NewRoutes(server)
	}

	walletLogger.Info("wallet service initialized",
		"mode", c.Mode,
		"network", network,
		"identityKey", storageIdentityKey[:16]+"...",
	)

	return svc, nil
}

// createWalletServicesConfig creates a wallet services config, optionally sharing ARC from Arcade.
func createWalletServicesConfig(network defs.BSVNetwork, arcade *arcadeconfig.Services) defs.WalletServices {
	// Start with defaults
	config := defs.DefaultServicesConfig(network)

	// If Arcade is available, share its ARC configuration
	if arcade != nil && arcade.Arcade != nil {
		// Arcade is running embedded, so we can share the ARC config
		// The wallet services will use the same ARC endpoint
		config.ArcConfig.Enabled = true
		// Note: arcade.Arcade contains the initialized Arcade instance
		// We use the default ARC config which should match what Arcade uses
	}

	return config
}

// Close closes the wallet service.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}

	if s.Provider != nil {
		s.Provider.Stop()
	}

	return nil
}
