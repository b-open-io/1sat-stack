package faucet

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/b-open-io/1sat-stack/pkg/wallet"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	walletcore "github.com/bsv-blockchain/go-wallet-toolbox/pkg/wallet"
)

// Service holds faucet business logic state.
type Service struct {
	Store           *Store
	Logger          *slog.Logger
	Config          *Config
	Wallet          *wallet.Services
	ServerPrivateKey string

	masterPubKeyHex string
	network         defs.BSVNetwork
}

// Services holds the initialized faucet service and optional routes.
type Services struct {
	Service *Service
	Routes  *Routes
	store   *Store
}

// InitializeDeps holds dependencies for faucet service initialization.
type InitializeDeps struct {
	Wallet           *wallet.Services
	ServerPrivateKey string
	Network          string
}

// Initialize creates faucet services from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	dataDir string,
	deps *InitializeDeps,
) (*Services, error) {
	if !c.Enabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	if deps.Wallet == nil {
		return nil, fmt.Errorf("wallet services are required for faucet")
	}

	if deps.ServerPrivateKey == "" {
		return nil, fmt.Errorf("server private key is required for faucet")
	}

	privKey, err := ec.PrivateKeyFromWif(deps.ServerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server private key: %w", err)
	}

	network := defs.NetworkMainnet
	if deps.Network == "test" {
		network = defs.NetworkTestnet
	}

	dbPath := c.DBPath
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(dataDir, dbPath)
	}

	store, err := NewStore(dbPath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to open faucet store: %w", err)
	}

	svc := &Service{
		Store:            store,
		Logger:           logger,
		Config:           c,
		Wallet:           deps.Wallet,
		ServerPrivateKey: deps.ServerPrivateKey,
		masterPubKeyHex:  hex.EncodeToString(privKey.PubKey().Compressed()),
		network:          network,
	}

	services := &Services{
		Service: svc,
		store:   store,
	}

	if c.Routes.Enabled {
		services.Routes = NewRoutes(svc, logger)
	}

	logger.Info("faucet service initialized",
		"db_path", dbPath,
		"default_drop_sats", c.DefaultDropSats,
		"master_pubkey", svc.masterPubKeyHex[:16]+"...",
	)

	return services, nil
}

// Close closes the faucet services.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

// DeriveInstanceMasterKey derives the per-faucet private key using BRC-42 key derivation.
func (s *Service) DeriveInstanceMasterKey(cfg *FaucetConfig) (*ec.PrivateKey, error) {
	parentPrivKey, err := ec.PrivateKeyFromWif(s.ServerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server private key: %w", err)
	}

	counterpartyPubKey, err := ec.PublicKeyFromString(cfg.DerivationCpPubKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse counterparty pubkey for %q: %w", cfg.FaucetName, err)
	}

	childPrivKey, err := parentPrivKey.DeriveChild(counterpartyPubKey, cfg.DerivationInvoiceNum)
	if err != nil {
		return nil, fmt.Errorf("failed to derive instance key for %q: %w", cfg.FaucetName, err)
	}

	return childPrivKey, nil
}

// GetFaucetWallet creates a wallet instance for the given faucet config.
func (s *Service) GetFaucetWallet(ctx context.Context, faucetName string) (*walletcore.Wallet, *FaucetConfig, error) {
	cfg, err := s.Store.LoadFaucetConfig(ctx, faucetName)
	if err != nil {
		return nil, nil, err
	}

	instancePriv, err := s.DeriveInstanceMasterKey(cfg)
	if err != nil {
		return nil, nil, err
	}

	w, err := walletcore.New(
		s.network,
		instancePriv,
		s.Wallet.Provider,
		walletcore.WithLogger(s.Logger),
		walletcore.WithServices(s.Wallet.WalletServices),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create faucet wallet for %q: %w", faucetName, err)
	}

	return w, cfg, nil
}

// GetFaucetWalletBySlug creates a wallet instance for the faucet identified by slug.
func (s *Service) GetFaucetWalletBySlug(ctx context.Context, slug string) (*walletcore.Wallet, *FaucetConfig, error) {
	cfg, err := s.Store.LoadFaucetConfigBySlug(ctx, slug)
	if err != nil {
		return nil, nil, err
	}

	w, _, err := s.GetFaucetWallet(ctx, cfg.FaucetName)
	if err != nil {
		return nil, nil, err
	}

	return w, cfg, nil
}

// FaucetBalance holds balance information for a faucet.
type FaucetBalance struct {
	BalanceSatoshis int64
	SpendableUTXOs  int64
}

// GetFaucetBalance returns the spendable balance for a faucet.
func (s *Service) GetFaucetBalance(ctx context.Context, faucetName string) (*FaucetBalance, error) {
	w, _, err := s.GetFaucetWallet(ctx, faucetName)
	if err != nil {
		return nil, fmt.Errorf("get faucet wallet for balance: %w", err)
	}

	limit := uint32(10000)
	result, err := w.ListOutputs(ctx, sdk.ListOutputsArgs{
		Basket: "default",
		Limit:  &limit,
	}, "")
	if err != nil {
		return nil, fmt.Errorf("list wallet outputs: %w", err)
	}

	balance := &FaucetBalance{}
	for _, out := range result.Outputs {
		if out.Spendable {
			balance.BalanceSatoshis += int64(out.Satoshis)
			balance.SpendableUTXOs++
		}
	}
	return balance, nil
}

// MasterPubKeyHex returns the hex-encoded compressed public key of the server key.
func (s *Service) MasterPubKeyHex() string {
	return s.masterPubKeyHex
}
