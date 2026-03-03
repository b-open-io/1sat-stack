package wallet

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	arcadeservice "github.com/bsv-blockchain/arcade/service"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/monitor"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	toolboxwallet "github.com/bsv-blockchain/go-wallet-toolbox/pkg/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

// Services holds initialized wallet services.
type Services struct {
	Provider       *storage.Provider
	Server         *storage.Server
	Wallet         sdk.Interface
	Routes         *Routes
	Monitor        *monitor.Daemon
	WalletServices wdk.Services
}

// InitializeDeps holds dependencies for wallet service initialization.
type InitializeDeps struct {
	Network     string                     // "main" or "test"
	Chaintracks chaintracks.Chaintracks    // local chain header ops + reorg/tip events
	Arcade      arcadeservice.ArcadeService // local broadcasting
	BeefStorage *beef.Storage              // local RawTx/MerklePath lookups
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

	// Expand ~ in database path
	c.ExpandDBPath()

	// Create logger with component name
	walletLogger := logging.NewComponentLogger(logger, "wallet", "")

	// Determine network
	network := defs.NetworkMainnet
	if deps.Network == "test" {
		network = defs.NetworkTestnet
	}

	// Create local wallet services implementing wdk.Services directly
	walletServices := NewLocalWalletServices(
		deps.Chaintracks,
		deps.Arcade,
		deps.BeefStorage,
		walletLogger,
	)

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

	// Wire monitor daemon
	var monitorDaemon *monitor.Daemon
	var monitorEventOpts []monitor.DaemonEventOption

	if deps.Chaintracks != nil {
		reorgCh := walletServices.SubscribeReorgs(ctx)
		monitorEventOpts = append(monitorEventOpts, monitor.WithReorgChannel(reorgCh))

		tipCh := walletServices.SubscribeTips(ctx)
		monitorEventOpts = append(monitorEventOpts, monitor.WithTipChannel(tipCh))
	}

	monitorDaemon, err = monitor.NewDaemonWithGORMLocker(
		ctx, walletLogger, provider, provider.Database.DB, monitorEventOpts...,
	)
	if err != nil {
		walletLogger.Warn("failed to create monitor daemon, continuing without it", "error", err)
	} else {
		monitorCfg := defs.DefaultMonitorConfig()
		if err := monitorDaemon.Start(ctx, monitorCfg.Tasks.EnabledTasks()); err != nil {
			walletLogger.Warn("failed to start monitor daemon", "error", err)
			monitorDaemon = nil
		} else {
			walletLogger.Info("wallet monitor daemon started")
		}
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
		Provider:       provider,
		Server:         server,
		Wallet:         serverWallet,
		Monitor:        monitorDaemon,
		WalletServices: walletServices,
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

// Close closes the wallet service.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}

	if s.Monitor != nil {
		if err := s.Monitor.Stop(); err != nil {
			_ = err
		}
	}

	if s.Provider != nil {
		s.Provider.Stop()
	}

	return nil
}
