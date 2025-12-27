package beef

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/dgraph-io/badger/v4"
)

var beefPrefix = []byte("beef:")

// DefaultBadgerPath returns the default path for badger BEEF storage.
func DefaultBadgerPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "./beef-badger/"
	}
	return filepath.Join(homeDir, ".1sat", "beef-badger")
}

type BadgerBeefStorage struct {
	db     *badger.DB
	ownsDB bool
}

// NewBadgerBeefStorage creates a new badger BEEF storage using an existing DB.
func NewBadgerBeefStorage(db *badger.DB) *BadgerBeefStorage {
	return &BadgerBeefStorage{db: db, ownsDB: false}
}

// NewBadgerBeefStorageFromPath creates a new badger BEEF storage, opening a new DB at the given path.
func NewBadgerBeefStorageFromPath(path string, logger *slog.Logger) (*BadgerBeefStorage, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	if logger != nil {
		logger.Info("opening BEEF badger database", "path", path)
	}

	start := time.Now()

	opts := badger.DefaultOptions(path)
	if logger != nil {
		opts = opts.WithLogger(&logging.BadgerLogger{
			Logger: logger.With("component", "beef-badger"),
			Level:  slog.LevelWarn,
		})
	}

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	if logger != nil {
		// Get LSM and vlog sizes from Badger
		lsmSize, vlogSize := db.Size()
		totalSize := lsmSize + vlogSize
		logger.Info("BEEF badger database opened",
			"path", path,
			"duration", time.Since(start).Round(time.Millisecond),
			"size_gb", fmt.Sprintf("%.2f", float64(totalSize)/(1024*1024*1024)),
		)
	}

	return &BadgerBeefStorage{db: db, ownsDB: true}, nil
}

func (b *BadgerBeefStorage) beefKey(txid *chainhash.Hash) []byte {
	key := make([]byte, len(beefPrefix)+32)
	copy(key, beefPrefix)
	copy(key[len(beefPrefix):], txid[:])
	return key
}

func (b *BadgerBeefStorage) Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	var beefBytes []byte
	err := b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(b.beefKey(txid))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			beefBytes = make([]byte, len(val))
			copy(beefBytes, val)
			return nil
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get beef: %w", err)
	}
	return beefBytes, nil
}

func (b *BadgerBeefStorage) Put(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Set(b.beefKey(txid), beefBytes)
	})
}

func (b *BadgerBeefStorage) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return nil, nil
}

func (b *BadgerBeefStorage) GetRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	beefBytes, err := b.Get(ctx, txid)
	if err != nil {
		return nil, err
	}
	rawTx, _, err := splitBEEF(beefBytes)
	return rawTx, err
}

func (b *BadgerBeefStorage) GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	beefBytes, err := b.Get(ctx, txid)
	if err != nil {
		return nil, err
	}
	_, proof, err := splitBEEF(beefBytes)
	if err != nil {
		return nil, err
	}
	if len(proof) == 0 {
		return nil, ErrNotFound
	}
	return proof, nil
}

func (b *BadgerBeefStorage) Close() error {
	if b.ownsDB && b.db != nil {
		return b.db.Close()
	}
	return nil
}
