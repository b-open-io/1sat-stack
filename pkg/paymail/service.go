package paymail

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/opns"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	arcadeservice "github.com/bsv-blockchain/arcade/service"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/wallet"
)

// BRC-29 protocol constants
var brc29Protocol = wallet.Protocol{
	SecurityLevel: 2,
	Protocol:      "3241645161d8",
}

// Service provides paymail resolution and payment derivation.
type Service struct {
	opns          *opns.LookupService
	ordfs         *ordfs.Ordfs
	arcade        arcadeservice.ArcadeService
	wallet        wallet.Interface
	beefStorage   *beef.Storage
	store         PendingStore
	anyoneDeriver *wallet.KeyDeriver
	logger        *slog.Logger
}

// NewService creates a new paymail service.
func NewService(
	opnsLookup *opns.LookupService,
	ordfsService *ordfs.Ordfs,
	arcadeService arcadeservice.ArcadeService,
	w wallet.Interface,
	beefStorage *beef.Storage,
	store PendingStore,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	anyonePriv, _ := wallet.AnyoneKey()
	return &Service{
		opns:          opnsLookup,
		ordfs:         ordfsService,
		arcade:        arcadeService,
		wallet:        w,
		beefStorage:   beefStorage,
		store:         store,
		anyoneDeriver: wallet.NewKeyDeriver(anyonePriv),
		logger:        logger,
	}
}

// ResolveIdentityKey resolves a paymail alias to an identity public key
// by looking up the OpNS origin and reading the MAP opns.idKey field via ORDFS.
func (s *Service) ResolveIdentityKey(ctx context.Context, alias string) (*ec.PublicKey, error) {
	outpoint, err := s.opns.Origin(ctx, alias)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve OpNS name %q: %w", alias, err)
	}
	if outpoint == nil {
		return nil, fmt.Errorf("no OpNS registration found for %q", alias)
	}

	// Load latest state via ORDFS (seq=-1 means latest)
	resolveCtx, resolveCancel := context.WithTimeout(ctx, ordfs.ResolveTimeout)
	defer resolveCancel()
	seq := -1
	resp, err := s.ordfs.Load(resolveCtx, &ordfs.Request{
		Outpoint: outpoint,
		Seq:      &seq,
		Map:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("ORDFS resolution failed for %q: %w", alias, err)
	}
	if resp == nil || resp.Map == nil {
		return nil, fmt.Errorf("no MAP data found for OpNS name %q", alias)
	}

	// Extract opns.idKey from merged MAP data
	var mapData map[string]string
	if err := json.Unmarshal(resp.Map, &mapData); err != nil {
		return nil, fmt.Errorf("failed to parse MAP data for %q: %w", alias, err)
	}

	idKeyHex, ok := mapData["opns.idKey"]
	if !ok || idKeyHex == "" {
		return nil, fmt.Errorf("no identity key registered for OpNS name %q", alias)
	}

	pubKeyBytes, err := hex.DecodeString(idKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid identity key hex for %q: %w", alias, err)
	}

	pubKey, err := ec.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid identity public key for %q: %w", alias, err)
	}

	return pubKey, nil
}

// DerivePaymentDestination generates a BRC-29 payment destination for the given
// identity key. Returns the pending payment record with derivation info, output script, etc.
func (s *Service) DerivePaymentDestination(ctx context.Context, alias, domain string, identityPubKey *ec.PublicKey, satoshis uint64) (*PendingPayment, error) {
	// Generate random prefix/suffix for BRC-29
	prefixBytes := make([]byte, 16)
	suffixBytes := make([]byte, 16)
	if _, err := rand.Read(prefixBytes); err != nil {
		return nil, fmt.Errorf("failed to generate derivation prefix: %w", err)
	}
	if _, err := rand.Read(suffixBytes); err != nil {
		return nil, fmt.Errorf("failed to generate derivation suffix: %w", err)
	}

	derivationPrefix := base64.StdEncoding.EncodeToString(prefixBytes)
	derivationSuffix := base64.StdEncoding.EncodeToString(suffixBytes)
	keyID := derivationPrefix + " " + derivationSuffix

	counterparty := wallet.Counterparty{
		Type:         wallet.CounterpartyTypeOther,
		Counterparty: identityPubKey,
	}
	paymentPubKey, err := s.anyoneDeriver.DerivePublicKey(brc29Protocol, keyID, counterparty, false)
	if err != nil {
		return nil, fmt.Errorf("failed to derive payment public key: %w", err)
	}

	address, err := script.NewAddressFromPublicKey(paymentPubKey, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create address from payment key: %w", err)
	}

	lockingScript, err := p2pkh.Lock(address)
	if err != nil {
		return nil, fmt.Errorf("failed to create locking script: %w", err)
	}

	now := time.Now()
	pending := &PendingPayment{
		Reference:        generateReference(),
		Alias:            alias,
		Domain:           domain,
		IdentityPubKey:   identityPubKey.ToDERHex(),
		DerivationPrefix: derivationPrefix,
		DerivationSuffix: derivationSuffix,
		Satoshis:         satoshis,
		OutputScript:     hex.EncodeToString(*lockingScript),
		CreatedAt:        now,
		ExpiresAt:        now.Add(defaultTTL),
	}

	if err := s.store.Create(ctx, pending); err != nil {
		return nil, fmt.Errorf("failed to store pending payment: %w", err)
	}

	return pending, nil
}

// Wallet returns the wallet interface for InternalizeAction calls.
func (s *Service) Wallet() wallet.Interface {
	return s.wallet
}

// Store returns the pending payment store.
func (s *Service) Store() PendingStore {
	return s.store
}

// BeefStorage returns the BEEF storage for ancestor lookups.
func (s *Service) BeefStorage() *beef.Storage {
	return s.beefStorage
}

// Arcade returns the arcade service for direct broadcast.
func (s *Service) Arcade() arcadeservice.ArcadeService {
	return s.arcade
}

// AnyoneDeriverIdentityKey returns the identity key of the anyone deriver (for PKI responses).
func (s *Service) AnyoneDeriverIdentityKey() *ec.PublicKey {
	return s.anyoneDeriver.IdentityKey()
}
