package paymail

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/broadcast"
	"github.com/b-open-io/1sat-stack/pkg/opns"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
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
	opns             *opns.LookupService
	ordfs            *ordfs.Ordfs
	handler          *broadcast.Handler
	messageBoxClient *MessageBoxClient
	beefStorage      *beef.Storage
	store            PendingStore
	anyoneDeriver    *wallet.KeyDeriver
	logger           *slog.Logger
}

// NewService creates a new paymail service.
func NewService(
	opnsLookup *opns.LookupService,
	ordfsService *ordfs.Ordfs,
	handler *broadcast.Handler,
	messageBoxClient *MessageBoxClient,
	beefStorage *beef.Storage,
	store PendingStore,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	anyonePriv, _ := wallet.AnyoneKey()
	return &Service{
		opns:             opnsLookup,
		ordfs:            ordfsService,
		handler:          handler,
		messageBoxClient: messageBoxClient,
		beefStorage:      beefStorage,
		store:            store,
		anyoneDeriver:    wallet.NewKeyDeriver(anyonePriv),
		logger:           logger,
	}
}

// ResolvedName is the payment-relevant state of an OpNS name: where the name
// ordinal currently rests and the identity key bound to it, if any.
type ResolvedName struct {
	IdentityKey *ec.PublicKey
	Outpoint    *transaction.Outpoint
}

// ResolveName resolves a paymail alias to its current OpNS state by looking up
// the OpNS origin and reading the merged MAP metadata via ORDFS. IdentityKey is
// nil when no opns.idKey is registered.
func (s *Service) ResolveName(ctx context.Context, alias string) (*ResolvedName, error) {
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
	if resp == nil {
		return nil, fmt.Errorf("ORDFS resolution returned no state for %q", alias)
	}

	resolved := &ResolvedName{Outpoint: resp.Outpoint}
	if resp.Map == nil {
		return resolved, nil
	}

	// Extract opns.idKey from merged MAP data
	var mapData map[string]string
	if err := json.Unmarshal(resp.Map, &mapData); err != nil {
		return nil, fmt.Errorf("failed to parse MAP data for %q: %w", alias, err)
	}

	idKeyHex, ok := mapData["opns.idKey"]
	if !ok || idKeyHex == "" {
		return resolved, nil
	}

	pubKeyBytes, err := hex.DecodeString(idKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid identity key hex for %q: %w", alias, err)
	}

	if resolved.IdentityKey, err = ec.PublicKeyFromBytes(pubKeyBytes); err != nil {
		return nil, fmt.Errorf("invalid identity public key for %q: %w", alias, err)
	}

	return resolved, nil
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

// ErrUndeliverable indicates the name has no registered identity key and its
// ordinal is not held in a plain P2PKH output, so no payment destination exists.
var ErrUndeliverable = errors.New("name has no identity key and is not held in a P2PKH output")

// DeriveFallbackDestination returns a payment destination paying directly to the
// P2PKH output that currently holds the name ordinal. This serves names without
// a registered identity key; there is no per-payment derivation and no message
// box delivery on this path.
func (s *Service) DeriveFallbackDestination(ctx context.Context, alias, domain string, current *transaction.Outpoint, satoshis uint64) (*PendingPayment, error) {
	if current == nil {
		return nil, fmt.Errorf("no current outpoint for %q", alias)
	}
	tx, err := s.beefStorage.LoadTx(ctx, &current.Txid)
	if err != nil {
		return nil, fmt.Errorf("failed to load current tx for %q: %w", alias, err)
	}
	if int(current.Index) >= len(tx.Outputs) {
		return nil, fmt.Errorf("outpoint index %d out of range for %q", current.Index, alias)
	}

	lockingScript := tx.Outputs[current.Index].LockingScript
	if !lockingScript.IsP2PKH() {
		return nil, fmt.Errorf("%q: %w", alias, ErrUndeliverable)
	}

	now := time.Now()
	pending := &PendingPayment{
		Reference:    generateReference(),
		Alias:        alias,
		Domain:       domain,
		Satoshis:     satoshis,
		OutputScript: hex.EncodeToString(*lockingScript),
		CreatedAt:    now,
		ExpiresAt:    now.Add(defaultTTL),
	}

	if err := s.store.Create(ctx, pending); err != nil {
		return nil, fmt.Errorf("failed to store pending payment: %w", err)
	}

	return pending, nil
}

// Store returns the pending payment store.
func (s *Service) Store() PendingStore {
	return s.store
}

// BeefStorage returns the BEEF storage for ancestor lookups.
func (s *Service) BeefStorage() *beef.Storage {
	return s.beefStorage
}

// Handler returns the broadcast handler — the broadcast chokepoint that
// captures BEEF and submits to arcade with the stack's callback token.
func (s *Service) Handler() *broadcast.Handler {
	return s.handler
}

// AnyoneDeriverIdentityKey returns the identity key of the anyone deriver (for PKI responses).
func (s *Service) AnyoneDeriverIdentityKey() *ec.PublicKey {
	return s.anyoneDeriver.IdentityKey()
}

// PaymailMessage is the message body stored in the message box for wallet clients to internalize.
type PaymailMessage struct {
	Beef              string `json:"beef"`
	OutputIndex       uint32 `json:"outputIndex"`
	DerivationPrefix  string `json:"derivationPrefix"`
	DerivationSuffix  string `json:"derivationSuffix"`
	SenderIdentityKey string `json:"senderIdentityKey"`
	Satoshis          uint64 `json:"satoshis"`
	Alias             string `json:"alias"`
}

// DeliverToMessageBox sends the payment data to the recipient's message box
// via the remote messagebox server for later internalization by the wallet client.
func (s *Service) DeliverToMessageBox(
	beefBytes []byte,
	outputIndex uint32,
	pending *PendingPayment,
) error {
	if pending.IdentityPubKey == "" {
		s.logger.Info("fallback payment to owner address, no message box delivery",
			"alias", pending.Alias,
			"satoshis", pending.Satoshis,
		)
		return nil
	}

	if s.messageBoxClient == nil {
		s.logger.Warn("messagebox not configured, skipping payment delivery",
			"alias", pending.Alias,
			"satoshis", pending.Satoshis,
		)
		return nil
	}

	msg := PaymailMessage{
		Beef:              hex.EncodeToString(beefBytes),
		OutputIndex:       outputIndex,
		DerivationPrefix:  pending.DerivationPrefix,
		DerivationSuffix:  pending.DerivationSuffix,
		SenderIdentityKey: s.AnyoneDeriverIdentityKey().ToDERHex(),
		Satoshis:          pending.Satoshis,
		Alias:             pending.Alias,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal paymail message: %w", err)
	}

	msgIDBytes := make([]byte, 16)
	if _, err := rand.Read(msgIDBytes); err != nil {
		return fmt.Errorf("failed to generate message ID: %w", err)
	}
	msgID := hex.EncodeToString(msgIDBytes)

	if err := s.messageBoxClient.SendMessage(
		context.Background(),
		pending.IdentityPubKey,
		"payment_inbox",
		msgID,
		body,
	); err != nil {
		return fmt.Errorf("failed to deliver to messagebox: %w", err)
	}

	s.logger.Info("payment delivered to message box",
		"alias", pending.Alias,
		"messageID", msgID,
		"satoshis", pending.Satoshis,
	)
	return nil
}
