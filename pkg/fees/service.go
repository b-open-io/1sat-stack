package fees

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	bip32 "github.com/bsv-blockchain/go-sdk/compat/bip32"
	bsvhash "github.com/bsv-blockchain/go-sdk/primitives/hash"
	"github.com/bsv-blockchain/go-sdk/script"
)

// HD key for address generation (matches 1sat-indexer and bsv21-overlay-1sat-sync)
const hdKeyString = "xpub661MyMwAqRbcF221R74MPqdipLsgUevAAX4hZP2rywyEeShpbe3v2r9ciAvSGT6FB22TEmFLdUyeEDJL4ekG8s9H5WXbzDQPr6eW1zEYYy9"

var hdKey *bip32.ExtendedKey

func init() {
	var err error
	hdKey, err = bip32.GetHDKeyFromExtendedPublicKey(hdKeyString)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize HD key: %v", err))
	}
}

// FeeService manages paid indexing for topics
type FeeService struct {
	store       store.Store
	outputStore *txo.OutputStore
	logger      *slog.Logger

	mu     sync.RWMutex
	topics map[string]string // topicId → address (in-memory)
}

// NewFeeService creates a new fee service
func NewFeeService(
	s store.Store,
	outputStore *txo.OutputStore,
	logger *slog.Logger,
) *FeeService {
	if logger == nil {
		logger = slog.Default()
	}

	return &FeeService{
		store:       s,
		outputStore: outputStore,
		logger:      logger.With("component", "fees"),
		topics:      make(map[string]string),
	}
}

// Register registers a topic for fee tracking
// Generates address from derivation seed
func (fs *FeeService) Register(ctx context.Context, topicID, derivationSeed string) (string, error) {
	address, err := fs.GenerateAddress(derivationSeed)
	if err != nil {
		return "", err
	}

	fs.mu.Lock()
	fs.topics[topicID] = address
	fs.mu.Unlock()

	fs.logger.Debug("topic registered", "topicID", topicID, "address", address)
	return address, nil
}

// Unregister removes a topic from fee tracking
func (fs *FeeService) Unregister(ctx context.Context, topicID string) {
	fs.mu.Lock()
	delete(fs.topics, topicID)
	fs.mu.Unlock()
}

// GetActiveTopics returns topics with positive balance or whitelisted (implements ovr.Storage interface)
func (fs *FeeService) GetActiveTopics(ctx context.Context) map[string]struct{} {
	result := make(map[string]struct{})

	// Get blacklist
	blacklist := make(map[string]struct{})
	if members, err := fs.store.SMembers(ctx, []byte(KeyBlacklist)); err == nil {
		for _, m := range members {
			blacklist[string(m)] = struct{}{}
		}
	}

	// Add whitelisted topics (not blacklisted)
	if members, err := fs.store.SMembers(ctx, []byte(KeyWhitelist)); err == nil {
		for _, m := range members {
			topic := string(m)
			if _, blocked := blacklist[topic]; !blocked {
				result[topic] = struct{}{}
			}
		}
	}

	// Check registered topics for positive balance
	fs.mu.RLock()
	topics := make(map[string]string, len(fs.topics))
	for k, v := range fs.topics {
		topics[k] = v
	}
	fs.mu.RUnlock()

	for topicID, address := range topics {
		// Skip blacklisted
		if _, blocked := blacklist[topicID]; blocked {
			continue
		}
		// Skip already whitelisted
		if _, ok := result[topicID]; ok {
			continue
		}
		// Check balance from indexed outputs
		if balance, err := fs.QueryBalance(ctx, address); err == nil && balance > 0 {
			result[topicID] = struct{}{}
		}
	}

	return result
}

// QueryBalance queries the current balance for an address from indexed outputs
func (fs *FeeService) QueryBalance(ctx context.Context, address string) (uint64, error) {
	cfg := txo.NewOutputSearchCfg().
		WithStringKeys("own:" + address).
		WithFilterSpent(true) // Only unspent outputs

	balance, _, err := fs.outputStore.SearchBalance(ctx, cfg)
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// GetAddressForTopic returns the fee address for a topic
func (fs *FeeService) GetAddressForTopic(topicID string) (string, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	addr, ok := fs.topics[topicID]
	return addr, ok
}

// GenerateAddress generates a Bitcoin address for a given derivation seed
// This matches the algorithm used in 1sat-indexer and bsv21-overlay-1sat-sync
func (fs *FeeService) GenerateAddress(derivationSeed string) (string, error) {
	// Hash the seed
	hash := sha256.Sum256([]byte(derivationSeed))

	// Generate BIP32 path: 21/{first_8_bytes>>1}/{bytes_24_to_28>>1}
	path := fmt.Sprintf("21/%d/%d",
		binary.BigEndian.Uint32(hash[:8])>>1,
		binary.BigEndian.Uint32(hash[24:])>>1)

	// Derive the key
	ek, err := hdKey.DeriveChildFromPath(path)
	if err != nil {
		return "", fmt.Errorf("failed to derive key for path %s: %w", path, err)
	}

	// Get the public key
	pubKey, err := ek.ECPubKey()
	if err != nil {
		return "", fmt.Errorf("failed to get public key: %w", err)
	}

	// Generate the PKHash
	pubKeyBytes := pubKey.Compressed()
	pkHash := bsvhash.Hash160(pubKeyBytes)

	// Convert to address (mainnet)
	address, err := script.NewAddressFromPublicKeyHash(pkHash, true)
	if err != nil {
		return "", fmt.Errorf("failed to create address: %w", err)
	}

	return address.AddressString, nil
}

// Whitelist management

// AddToWhitelist adds a topic to the whitelist
func (fs *FeeService) AddToWhitelist(ctx context.Context, topicID string) error {
	return fs.store.SAdd(ctx, []byte(KeyWhitelist), []byte(topicID))
}

// RemoveFromWhitelist removes a topic from the whitelist
func (fs *FeeService) RemoveFromWhitelist(ctx context.Context, topicID string) error {
	return fs.store.SRem(ctx, []byte(KeyWhitelist), []byte(topicID))
}

// AddToBlacklist adds a topic to the blacklist
func (fs *FeeService) AddToBlacklist(ctx context.Context, topicID string) error {
	return fs.store.SAdd(ctx, []byte(KeyBlacklist), []byte(topicID))
}

// RemoveFromBlacklist removes a topic from the blacklist
func (fs *FeeService) RemoveFromBlacklist(ctx context.Context, topicID string) error {
	return fs.store.SRem(ctx, []byte(KeyBlacklist), []byte(topicID))
}

// GetWhitelist returns the list of whitelisted topics
func (fs *FeeService) GetWhitelist(ctx context.Context) ([]string, error) {
	members, err := fs.store.SMembers(ctx, []byte(KeyWhitelist))
	if err != nil {
		return nil, err
	}
	result := make([]string, len(members))
	for i, m := range members {
		result[i] = string(m)
	}
	return result, nil
}

// GetBlacklist returns the list of blacklisted topics
func (fs *FeeService) GetBlacklist(ctx context.Context) ([]string, error) {
	members, err := fs.store.SMembers(ctx, []byte(KeyBlacklist))
	if err != nil {
		return nil, err
	}
	result := make([]string, len(members))
	for i, m := range members {
		result[i] = string(m)
	}
	return result, nil
}
