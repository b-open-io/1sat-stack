package paymail

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const defaultTTL = 15 * time.Minute

// PendingPayment holds the state for a payment that has been requested
// but not yet received.
type PendingPayment struct {
	Reference        string    `json:"reference"`
	IdentityPubKey   string    `json:"identityPubKey"`
	DerivationPrefix string    `json:"derivationPrefix"`
	DerivationSuffix string    `json:"derivationSuffix"`
	Satoshis         uint64    `json:"satoshis"`
	OutputScript     string    `json:"outputScript"`
	CreatedAt        time.Time `json:"createdAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

// Store is a thread-safe in-memory store for pending payment references.
type Store struct {
	mu       sync.RWMutex
	payments map[string]*PendingPayment
}

// NewStore creates a new pending payment store and starts the cleanup goroutine.
func NewStore() *Store {
	s := &Store{
		payments: make(map[string]*PendingPayment),
	}
	go s.cleanupLoop()
	return s
}

// Create stores a new pending payment and returns it.
func (s *Store) Create(
	identityPubKey string,
	derivationPrefix string,
	derivationSuffix string,
	satoshis uint64,
	outputScript string,
) *PendingPayment {
	ref := generateReference()
	now := time.Now()

	p := &PendingPayment{
		Reference:        ref,
		IdentityPubKey:   identityPubKey,
		DerivationPrefix: derivationPrefix,
		DerivationSuffix: derivationSuffix,
		Satoshis:         satoshis,
		OutputScript:     outputScript,
		CreatedAt:        now,
		ExpiresAt:        now.Add(defaultTTL),
	}

	s.mu.Lock()
	s.payments[ref] = p
	s.mu.Unlock()
	return p
}

// Get retrieves a pending payment by reference. Returns nil if not found or expired.
func (s *Store) Get(reference string) *PendingPayment {
	s.mu.RLock()
	p, ok := s.payments[reference]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Now().After(p.ExpiresAt) {
		s.Delete(reference)
		return nil
	}
	return p
}

// Delete removes a pending payment by reference.
func (s *Store) Delete(reference string) {
	s.mu.Lock()
	delete(s.payments, reference)
	s.mu.Unlock()
}

// cleanupLoop periodically removes expired entries.
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for ref, p := range s.payments {
			if now.After(p.ExpiresAt) {
				delete(s.payments, ref)
			}
		}
		s.mu.Unlock()
	}
}

func generateReference() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
