package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdkauth "github.com/bsv-blockchain/go-sdk/auth"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/dgraph-io/badger/v4"
)

const defaultSessionTTL = 24 * time.Hour

// storedSession is the JSON-serializable form of sdkauth.PeerSession.
type storedSession struct {
	IsAuthenticated    bool   `json:"a"`
	SessionNonce       string `json:"n"`
	PeerNonce          string `json:"p"`
	PeerIdentityKeyHex string `json:"k,omitempty"`
	LastUpdate         int64  `json:"u"`
}

func sessionFromPeer(s *sdkauth.PeerSession) *storedSession {
	ss := &storedSession{
		IsAuthenticated: s.IsAuthenticated,
		SessionNonce:    s.SessionNonce,
		PeerNonce:       s.PeerNonce,
		LastUpdate:      s.LastUpdate,
	}
	if s.PeerIdentityKey != nil {
		ss.PeerIdentityKeyHex = s.PeerIdentityKey.ToDERHex()
	}
	return ss
}

func (ss *storedSession) toPeer() (*sdkauth.PeerSession, error) {
	ps := &sdkauth.PeerSession{
		IsAuthenticated: ss.IsAuthenticated,
		SessionNonce:    ss.SessionNonce,
		PeerNonce:       ss.PeerNonce,
		LastUpdate:      ss.LastUpdate,
	}
	if ss.PeerIdentityKeyHex != "" {
		key, err := ec.PublicKeyFromString(ss.PeerIdentityKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid stored identity key: %w", err)
		}
		ps.PeerIdentityKey = key
	}
	return ps, nil
}

// Key prefixes for session storage.
var (
	noncePrefix    = []byte("n:")
	identityPrefix = []byte("id:")
)

// BadgerSessionManager implements sdkauth.SessionManager backed by a standalone Badger instance.
type BadgerSessionManager struct {
	db     *badger.DB
	ttl    time.Duration
	logger *slog.Logger
}

// NewBadgerSessionManager opens a Badger database at the given path and returns a session manager.
func NewBadgerSessionManager(dbPath string, ttl time.Duration, logger *slog.Logger) (*BadgerSessionManager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if ttl == 0 {
		ttl = defaultSessionTTL
	}

	path := expandPath(dbPath)
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, fmt.Errorf("failed to create session store directory: %w", err)
	}

	opts := badger.DefaultOptions(path)
	opts = opts.WithLogger(nil) // suppress badger logs

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open session store: %w", err)
	}

	return &BadgerSessionManager{
		db:     db,
		ttl:    ttl,
		logger: logger,
	}, nil
}

func (sm *BadgerSessionManager) AddSession(session *sdkauth.PeerSession) error {
	if session.SessionNonce == "" {
		return errors.New("invalid session: sessionNonce is required")
	}

	data, err := json.Marshal(sessionFromPeer(session))
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	return sm.db.Update(func(txn *badger.Txn) error {
		// Store session by nonce
		entry := badger.NewEntry(nonceKey(session.SessionNonce), data).WithTTL(sm.ttl)
		if err := txn.SetEntry(entry); err != nil {
			return err
		}

		// Index by identity key
		if session.PeerIdentityKey != nil {
			idxKey := identityIndexKey(session.PeerIdentityKey.ToDERHex(), session.SessionNonce)
			entry := badger.NewEntry(idxKey, nil).WithTTL(sm.ttl)
			if err := txn.SetEntry(entry); err != nil {
				return err
			}
		}

		return nil
	})
}

func (sm *BadgerSessionManager) UpdateSession(session *sdkauth.PeerSession) {
	sm.RemoveSession(session)
	if err := sm.AddSession(session); err != nil {
		sm.logger.Error("failed to update session", "nonce", session.SessionNonce, "error", err)
	}
}

func (sm *BadgerSessionManager) GetSession(identifier string) (*sdkauth.PeerSession, error) {
	// Try direct nonce lookup first
	var data []byte
	err := sm.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(nonceKey(identifier))
		if err != nil {
			return err
		}
		data, err = item.ValueCopy(nil)
		return err
	})
	if err == nil {
		var ss storedSession
		if err := json.Unmarshal(data, &ss); err != nil {
			return nil, fmt.Errorf("failed to unmarshal session: %w", err)
		}
		return ss.toPeer()
	}
	if !errors.Is(err, badger.ErrKeyNotFound) {
		return nil, fmt.Errorf("session lookup failed: %w", err)
	}

	// Try identity key lookup — scan prefix id:{identifier}:
	prefix := append(identityPrefix, []byte(identifier+":")...)
	var best *sdkauth.PeerSession

	err = sm.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			key := it.Item().Key()
			// Extract nonce from id:{keyHex}:{nonce}
			nonce := string(key[len(prefix):])

			item, err := txn.Get(nonceKey(nonce))
			if errors.Is(err, badger.ErrKeyNotFound) {
				continue
			}
			if err != nil {
				return err
			}

			sessionData, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}

			var ss storedSession
			if err := json.Unmarshal(sessionData, &ss); err != nil {
				continue
			}

			ps, err := ss.toPeer()
			if err != nil {
				continue
			}

			if best == nil ||
				(ps.IsAuthenticated && !best.IsAuthenticated) ||
				(ps.LastUpdate > best.LastUpdate && (ps.IsAuthenticated || !best.IsAuthenticated)) {
				best = ps
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("identity session scan failed: %w", err)
	}

	if best != nil {
		return best, nil
	}
	return nil, errors.New("session-not-found")
}

func (sm *BadgerSessionManager) RemoveSession(session *sdkauth.PeerSession) {
	_ = sm.db.Update(func(txn *badger.Txn) error {
		if session.SessionNonce != "" {
			_ = txn.Delete(nonceKey(session.SessionNonce))
		}
		if session.PeerIdentityKey != nil {
			_ = txn.Delete(identityIndexKey(session.PeerIdentityKey.ToDERHex(), session.SessionNonce))
		}
		return nil
	})
}

func (sm *BadgerSessionManager) HasSession(identifier string) bool {
	// Check nonce
	err := sm.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(nonceKey(identifier))
		return err
	})
	if err == nil {
		return true
	}

	// Check identity key prefix
	prefix := append(identityPrefix, []byte(identifier+":")...)
	found := false
	_ = sm.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		it.Rewind()
		if it.Valid() {
			found = true
		}
		return nil
	})
	return found
}

// Close closes the underlying Badger database.
func (sm *BadgerSessionManager) Close() error {
	return sm.db.Close()
}

func nonceKey(nonce string) []byte {
	return append(noncePrefix, []byte(nonce)...)
}

func identityIndexKey(keyHex, nonce string) []byte {
	return append(identityPrefix, []byte(keyHex+":"+nonce)...)
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
