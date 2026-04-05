package wallet

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	arcadeservice "github.com/bsv-blockchain/arcade/service"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/monitor"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	toolboxwallet "github.com/bsv-blockchain/go-wallet-toolbox/pkg/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

// Services holds initialized wallet services.
type Services struct {
	Provider       wdk.WalletStorageProvider
	Wallet         sdk.Interface
	Monitor        *monitor.Daemon
	WalletServices wdk.Services
	IdentityKey    string
}

// InitializeDeps holds dependencies for wallet service initialization.
type InitializeDeps struct {
	Network     string                      // "main" or "test"
	Chaintracks chaintracks.Chaintracks     // local chain header ops + reorg/tip events
	Arcade      arcadeservice.ArcadeService // local broadcasting
	BeefStorage *beef.Storage               // local RawTx/MerklePath lookups
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

	walletLogger := logging.NewComponentLogger(logger, "wallet", "")

	network := defs.NetworkMainnet
	if deps.Network == "test" {
		network = defs.NetworkTestnet
	}

	storageIdentityKey, err := wdk.IdentityKey(c.ServerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage identity key: %w", err)
	}

	switch c.Mode {
	case ModeEmbedded:
		return c.initEmbedded(ctx, walletLogger, deps, network, storageIdentityKey)
	case ModeRemote:
		return c.initRemote(ctx, walletLogger, network, storageIdentityKey)
	default:
		return nil, fmt.Errorf("unknown wallet mode: %s", c.Mode)
	}
}

func (c *Config) initEmbedded(
	ctx context.Context,
	logger *slog.Logger,
	deps *InitializeDeps,
	network defs.BSVNetwork,
	storageIdentityKey string,
) (*Services, error) {
	c.ExpandDBPath()

	walletServices := NewLocalWalletServices(
		deps.Chaintracks,
		deps.Arcade,
		deps.BeefStorage,
		logger,
	)

	provider, err := storage.NewGORMProvider(network, walletServices,
		storage.WithDBConfig(c.DB),
		storage.WithFeeModel(c.FeeModel),
		storage.WithLogger(logger),
		storage.WithBackgroundBroadcasterContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage provider: %w", err)
	}

	name := c.Name
	if name == "" {
		name = "1sat-wallet"
	}
	if _, err = provider.Migrate(ctx, name, storageIdentityKey); err != nil {
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
		ctx, logger, provider, provider.Database.DB, monitorEventOpts...,
	)
	if err != nil {
		logger.Warn("failed to create monitor daemon, continuing without it", "error", err)
	} else {
		monitorCfg := defs.DefaultMonitorConfig()
		if err := monitorDaemon.Start(ctx, monitorCfg.Tasks.EnabledTasks()); err != nil {
			logger.Warn("failed to start monitor daemon", "error", err)
			monitorDaemon = nil
		} else {
			logger.Info("wallet monitor daemon started")
		}
	}

	serverWallet, err := toolboxwallet.New(
		network,
		c.ServerPrivateKey,
		provider,
		toolboxwallet.WithLogger(logger),
		toolboxwallet.WithServices(walletServices),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create server wallet: %w", err)
	}

	logger.Info("wallet service initialized",
		"mode", ModeEmbedded,
		"network", network,
		"identityKey", storageIdentityKey[:16]+"...",
	)

	return &Services{
		Provider:       provider,
		Wallet:         serverWallet,
		Monitor:        monitorDaemon,
		WalletServices: walletServices,
		IdentityKey:    storageIdentityKey,
	}, nil
}

func (c *Config) initRemote(
	ctx context.Context,
	logger *slog.Logger,
	network defs.BSVNetwork,
	storageIdentityKey string,
) (*Services, error) {
	if c.RemoteURL == "" {
		return nil, fmt.Errorf("remote_url is required for remote wallet mode")
	}

	// Create a completed proto-wallet for auth with the remote storage server
	privKey, err := ec.PrivateKeyFromHex(c.ServerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server private key: %w", err)
	}
	protoWallet, err := sdk.NewCompletedProtoWallet(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create proto wallet: %w", err)
	}

	remoteStorage, cleanup, err := storage.NewClient(c.RemoteURL, protoWallet)
	if err != nil {
		return nil, fmt.Errorf("failed to create remote storage client: %w", err)
	}
	_ = cleanup // TODO: call on Close

	serverWallet, err := toolboxwallet.New(
		network,
		c.ServerPrivateKey,
		remoteStorage,
		toolboxwallet.WithLogger(logger),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create server wallet: %w", err)
	}

	logger.Info("wallet service initialized",
		"mode", ModeRemote,
		"network", network,
		"remoteURL", c.RemoteURL,
		"identityKey", storageIdentityKey[:16]+"...",
	)

	return &Services{
		Provider:    remoteStorage,
		Wallet:      serverWallet,
		IdentityKey: storageIdentityKey,
	}, nil
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

	if p, ok := s.Provider.(*storage.Provider); ok && p != nil {
		p.Stop()
	}

	return nil
}
