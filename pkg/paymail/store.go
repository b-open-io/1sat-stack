package paymail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

const defaultTTL = 15 * time.Minute

// PendingPayment holds the state for a payment that has been requested
// but not yet received.
type PendingPayment struct {
	Reference        string    `json:"reference"`
	Alias            string    `json:"alias"`
	Domain           string    `json:"domain"`
	IdentityPubKey   string    `json:"identityPubKey"`
	DerivationPrefix string    `json:"derivationPrefix"`
	DerivationSuffix string    `json:"derivationSuffix"`
	Satoshis         uint64    `json:"satoshis"`
	OutputScript     string    `json:"outputScript"`
	TxID             []byte    `json:"txid,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

// PendingStore persists pending payment references across restarts.
type PendingStore interface {
	Create(ctx context.Context, p *PendingPayment) error
	Get(ctx context.Context, reference string) (*PendingPayment, error)
	Update(ctx context.Context, p *PendingPayment) error
	Delete(ctx context.Context, reference string) error
	Close() error
}

func generateReference() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
