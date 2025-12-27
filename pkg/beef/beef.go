package beef

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/dedup"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
)

var BeefKey = "beef"
var ErrNotFound = errors.New("not-found")
var ErrInvalidMerkleProof = errors.New("invalid merkle proof")
var ErrMissingInputs = errors.New("missing required input transactions")

// splitBEEF extracts raw transaction and merkle proof from BEEF bytes
func splitBEEF(beefBytes []byte) (rawTx []byte, proof []byte, err error) {
	_, tx, _, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return nil, nil, err
	}
	if tx == nil {
		return nil, nil, errors.New("no transaction in BEEF")
	}

	rawTx = tx.Bytes()
	if tx.MerklePath != nil {
		proof = tx.MerklePath.Bytes()
	}
	return rawTx, proof, nil
}

// assembleBEEF creates BEEF bytes from raw transaction and optional merkle proof
func assembleBEEF(txid *chainhash.Hash, rawTx []byte, proof []byte) ([]byte, error) {
	tx, err := transaction.NewTransactionFromBytes(rawTx)
	if err != nil {
		return nil, err
	}

	if len(proof) > 0 {
		mp, err := transaction.NewMerklePathFromBinary(proof)
		if err != nil {
			return nil, err
		}
		tx.MerklePath = mp
	}

	beef := &transaction.Beef{
		Version:      transaction.BEEF_V2,
		BUMPs:        []*transaction.MerklePath{},
		Transactions: make(map[chainhash.Hash]*transaction.BeefTx),
	}

	beefTx := &transaction.BeefTx{
		Transaction: tx,
		BumpIndex:   -1,
	}

	if tx.MerklePath != nil {
		beef.BUMPs = append(beef.BUMPs, tx.MerklePath)
		beefTx.BumpIndex = 0
		beefTx.DataFormat = transaction.RawTxAndBumpIndex
	} else {
		beefTx.DataFormat = transaction.RawTx
	}

	beef.Transactions[*txid] = beefTx

	return beef.AtomicBytes(txid)
}

// BaseBeefStorage is a simple key/value storage interface for BEEF data
type BaseBeefStorage interface {
	Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error)
	Put(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error
	UpdateMerklePath(ctx context.Context, txid *chainhash.Hash) ([]byte, error)
	GetRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error)
	GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error)
	Close() error
}

// Storage is the main implementation that handles all high-level BEEF operations
type Storage struct {
	storages     []BaseBeefStorage
	loader       *dedup.Loader[chainhash.Hash, []byte]
	saver        *dedup.Saver[chainhash.Hash, []byte]
	chainTracker chaintracker.ChainTracker
}

// NewStorageFromProviders creates a new Storage from pre-configured providers
func NewStorageFromProviders(storages []BaseBeefStorage, chainTracker chaintracker.ChainTracker) *Storage {
	s := &Storage{
		storages:     storages,
		chainTracker: chainTracker,
	}

	s.loader = dedup.NewLoader(func(txid chainhash.Hash) ([]byte, error) {
		return s.loadBeefInternal(context.Background(), &txid)
	})

	s.saver = dedup.NewSaver(func(txid chainhash.Hash, beefBytes []byte) error {
		return s.saveBeefInternal(context.Background(), &txid, beefBytes)
	})

	return s
}

// NewStorage creates a new Storage from a connection string with optional SPV validation.
// Deprecated: Use NewStorageFromProviders or Config.Initialize instead.
func NewStorage(connectionString string, chainTracker chaintracker.ChainTracker) (*Storage, error) {
	storages, err := parseConnectionString(connectionString)
	if err != nil {
		return nil, err
	}

	return NewStorageFromProviders(storages, chainTracker), nil
}

// LoadBeef loads a BEEF from storage.
func (s *Storage) LoadBeef(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return s.loader.Load(*txid)
}

// MergeBeef takes an existing Beef object and merges in additional transactions by their txids.
func (s *Storage) MergeBeef(ctx context.Context, beef *transaction.Beef, txids []*chainhash.Hash) (*transaction.Beef, error) {
	if len(txids) == 0 {
		return beef, nil
	}

	for _, txid := range txids {
		additionalBeef, err := s.loader.Load(*txid)
		if err != nil {
			return nil, errors.New("failed to load tx for merge " + txid.String() + ": " + err.Error())
		}
		if err := beef.MergeBeefBytes(additionalBeef); err != nil {
			return nil, err
		}
	}

	return beef, nil
}

func (s *Storage) loadBeefInternal(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	var beefBytes []byte
	var err error

	for i, storage := range s.storages {
		beefBytes, err = storage.Get(ctx, txid)
		if err == nil {
			for j := 0; j < i; j++ {
				s.storages[j].Put(ctx, txid, beefBytes)
			}
			break
		}
		if err != ErrNotFound {
			return nil, err
		}
	}

	if beefBytes == nil {
		return nil, errors.New("transaction " + txid.String() + " not found in BEEF")
	}

	beef, tx, _, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return nil, err
	}

	if tx == nil {
		return nil, errors.New("transaction " + txid.String() + " not found in BEEF")
	}

	if tx.MerklePath == nil {
		for _, input := range tx.Inputs {
			inputBeef, err := s.loader.Load(*input.SourceTXID)
			if err != nil {
				return nil, errors.New(ErrMissingInputs.Error() + ": " + input.SourceTXID.String())
			}
			if err := beef.MergeBeefBytes(inputBeef); err != nil {
				return nil, err
			}
		}
	}

	if s.chainTracker != nil {
		needsUpdate := false

		if tx.MerklePath == nil {
			needsUpdate = true
		} else {
			valid, err := tx.MerklePath.Verify(ctx, txid, s.chainTracker)
			if err != nil || !valid {
				needsUpdate = true
			}
		}

		if needsUpdate {
			updatedBeef, err := s.UpdateMerklePath(ctx, txid, s.chainTracker)
			if err != nil {
				return nil, err
			}
			if err := beef.MergeBeefBytes(updatedBeef); err != nil {
				return nil, err
			}
		}
	}

	return beef.AtomicBytes(txid)
}

// SaveBeef saves a BEEF by decomposing it into individual transactions
func (s *Storage) SaveBeef(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	return s.saver.Save(*txid, beefBytes)
}

func (s *Storage) saveBeefInternal(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	beef, tx, parsedTxid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return errors.New("failed to parse BEEF: " + err.Error())
	}

	var mainTx *transaction.Transaction
	if tx != nil {
		mainTx = tx
	} else if beef != nil && parsedTxid != nil {
		mainTx = beef.FindTransactionByHash(parsedTxid)
	} else if beef != nil && txid != nil {
		mainTx = beef.FindTransactionByHash(txid)
	}

	if mainTx == nil {
		return errors.New("could not find transaction in BEEF")
	}

	if beef != nil {
		for txHash, beefTx := range beef.Transactions {
			if beefTx.Transaction == nil {
				continue
			}

			individualBeef, err := s.createIndividualBEEF(&txHash, beefTx.Transaction)
			if err != nil {
				return errors.New("failed to create individual BEEF for " + txHash.String() + ": " + err.Error())
			}

			for _, storage := range s.storages {
				if err := storage.Put(ctx, &txHash, individualBeef); err != nil {
					return errors.New("failed to save to storage: " + err.Error())
				}
			}
		}
	} else {
		for _, storage := range s.storages {
			if err := storage.Put(ctx, txid, beefBytes); err != nil {
				return errors.New("failed to save to storage: " + err.Error())
			}
		}
	}

	return nil
}

func (s *Storage) createIndividualBEEF(txid *chainhash.Hash, tx *transaction.Transaction) ([]byte, error) {
	beef := &transaction.Beef{
		Version:      transaction.BEEF_V2,
		BUMPs:        []*transaction.MerklePath{},
		Transactions: make(map[chainhash.Hash]*transaction.BeefTx),
	}

	beefTx := &transaction.BeefTx{
		Transaction: tx,
		BumpIndex:   -1,
	}

	if tx.MerklePath != nil {
		beef.BUMPs = append(beef.BUMPs, tx.MerklePath)
		beefTx.BumpIndex = 0
		beefTx.DataFormat = transaction.RawTxAndBumpIndex
	} else {
		beefTx.DataFormat = transaction.RawTx
	}

	beef.Transactions[*txid] = beefTx

	return beef.AtomicBytes(txid)
}

// UpdateMerklePath attempts to fetch an updated BEEF with merkle proof
func (s *Storage) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash, ct chaintracker.ChainTracker) ([]byte, error) {
	for _, storage := range s.storages {
		updatedBeef, err := storage.UpdateMerklePath(ctx, txid)
		if err != nil {
			continue
		}
		if len(updatedBeef) > 0 {
			if ct != nil {
				_, tx, _, parseErr := transaction.ParseBeef(updatedBeef)
				if parseErr == nil && tx != nil && tx.MerklePath != nil {
					valid, verifyErr := tx.MerklePath.Verify(ctx, txid, ct)
					if verifyErr == nil && valid {
						if err := s.saveBeefInternal(ctx, txid, updatedBeef); err != nil {
							return nil, fmt.Errorf("failed to save updated BEEF: %w", err)
						}
						return updatedBeef, nil
					}
				}
			} else {
				if err := s.saveBeefInternal(ctx, txid, updatedBeef); err != nil {
					return nil, fmt.Errorf("failed to save updated BEEF: %w", err)
				}
				return updatedBeef, nil
			}
		}
	}
	return nil, errors.New("unable to fetch updated merkle proof for " + txid.String())
}

func (s *Storage) Close() error {
	if s.loader != nil {
		s.loader.Clear()
	}
	if s.saver != nil {
		s.saver.Clear()
	}

	var errs []error
	for _, storage := range s.storages {
		if err := storage.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.New("errors closing storages")
	}
	return nil
}

// LoadTx loads a transaction from the storage
func (s *Storage) LoadTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	beefBytes, err := s.LoadBeef(ctx, txid)
	if err != nil {
		return nil, err
	}

	return s.LoadTxFromBeef(ctx, beefBytes, txid)
}

// LoadTxFromBeef loads a transaction from BEEF bytes
func (s *Storage) LoadTxFromBeef(ctx context.Context, beefBytes []byte, txid *chainhash.Hash) (*transaction.Transaction, error) {
	beef, tx, parsedTxid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return nil, err
	}

	if parsedTxid != nil && !parsedTxid.IsEqual(txid) {
		return nil, errors.New("txid mismatch: requested " + txid.String() + ", got " + parsedTxid.String())
	}

	if tx == nil && beef != nil {
		tx = beef.FindTransaction(txid.String())
	}

	if tx == nil {
		return nil, errors.New("transaction " + txid.String() + " not found in BEEF")
	}

	return tx, nil
}

func (s *Storage) BuildBeefTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	tx, err := s.LoadTx(ctx, txid)
	if err != nil {
		return nil, err
	}

	if tx.MerklePath == nil {
		for _, input := range tx.Inputs {
			input.SourceTransaction, err = s.BuildBeefTx(ctx, input.SourceTXID)
			if err != nil {
				return nil, err
			}
		}
	}

	return tx, nil
}

// BuildFullBeefTx loads a transaction with all input source transactions populated,
// regardless of whether the main transaction has a merkle path.
// This is needed for overlay submission where inputs must be validated.
func (s *Storage) BuildFullBeefTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	tx, err := s.LoadTx(ctx, txid)
	if err != nil {
		return nil, err
	}

	for _, input := range tx.Inputs {
		if input.SourceTransaction != nil {
			continue
		}
		input.SourceTransaction, err = s.BuildBeefTx(ctx, input.SourceTXID)
		if err != nil {
			return nil, err
		}
	}

	return tx, nil
}

func (s *Storage) BuildFullBeef(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	beefBytes, err := s.LoadBeef(ctx, txid)
	if err != nil {
		return nil, err
	}
	beef, tx, txid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return nil, err
	}
	for _, input := range tx.Inputs {
		if input.SourceTransaction != nil {
			continue
		}
		input.SourceTransaction, err = s.BuildBeefTx(ctx, input.SourceTXID)
		if err != nil {
			return nil, err
		}
		_, err = beef.MergeTransaction(input.SourceTransaction)
		if err != nil {
			return nil, err
		}
	}

	return beef.AtomicBytes(txid)
}

// LoadRawTx loads just the raw transaction bytes
func (s *Storage) LoadRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	for i, storage := range s.storages {
		rawTx, err := storage.GetRawTx(ctx, txid)
		if err == nil {
			if i > 0 {
				if beefBytes, err := assembleBEEF(txid, rawTx, nil); err == nil {
					for j := 0; j < i; j++ {
						s.storages[j].Put(ctx, txid, beefBytes)
					}
				}
			}
			return rawTx, nil
		}
		if err != ErrNotFound {
			return nil, err
		}
	}
	return nil, ErrNotFound
}

// LoadProof loads just the merkle proof bytes
func (s *Storage) LoadProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	for i, storage := range s.storages {
		proof, err := storage.GetProof(ctx, txid)
		if err == nil {
			if i > 0 {
				if rawTx, err := storage.GetRawTx(ctx, txid); err == nil {
					if beefBytes, err := assembleBEEF(txid, rawTx, proof); err == nil {
						for j := 0; j < i; j++ {
							s.storages[j].Put(ctx, txid, beefBytes)
						}
					}
				}
			}
			return proof, nil
		}
		if err != ErrNotFound {
			return nil, err
		}
	}
	return nil, ErrNotFound
}

func expandHomePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		return filepath.Join(homeDir, path[2:]), nil
	}
	return path, nil
}

func parseConnectionString(connectionString string) ([]BaseBeefStorage, error) {
	var connectionStrings []string

	if connectionString != "" {
		if strings.HasPrefix(strings.TrimSpace(connectionString), "[") {
			if err := json.Unmarshal([]byte(connectionString), &connectionStrings); err != nil {
				return nil, fmt.Errorf("invalid JSON array for BEEF storage: %w", err)
			}
		} else if strings.Contains(connectionString, ",") {
			connectionStrings = strings.Split(connectionString, ",")
			for i, s := range connectionStrings {
				connectionStrings[i] = strings.TrimSpace(s)
			}
		} else {
			connectionStrings = []string{connectionString}
		}
	}

	if len(connectionStrings) == 0 {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			connectionStrings = []string{"./beef/"}
		} else {
			dotOneSatDir := filepath.Join(homeDir, ".1sat")
			if err := os.MkdirAll(dotOneSatDir, 0755); err != nil {
				connectionStrings = []string{"./beef/"}
			} else {
				connectionStrings = []string{filepath.Join(dotOneSatDir, "beef")}
			}
		}
	}

	var storages []BaseBeefStorage

	for _, connectionString := range connectionStrings {
		connectionString = strings.TrimSpace(connectionString)

		var storage BaseBeefStorage

		switch {
		case strings.HasPrefix(connectionString, "lru://"):
			u, err := url.Parse(connectionString)
			if err != nil {
				return nil, fmt.Errorf("invalid LRU URL format: %w", err)
			}

			sizeStr := u.Query().Get("size")
			if sizeStr == "" {
				return nil, fmt.Errorf("LRU size not specified, use format: lru://?size=100mb")
			}

			size, err := ParseSize(sizeStr)
			if err != nil {
				return nil, fmt.Errorf("invalid LRU size format %s: %w", sizeStr, err)
			}
			storage = NewLRUBeefStorage(size)

		case strings.HasPrefix(connectionString, "junglebus"):
			return nil, fmt.Errorf("junglebus provider requires using Config.Initialize() with a junglebus client")

		case filepath.IsAbs(connectionString) || strings.HasPrefix(connectionString, "./") || strings.HasPrefix(connectionString, "../") || strings.HasPrefix(connectionString, "~/"):
			expandedPath, err := expandHomePath(connectionString)
			if err != nil {
				return nil, err
			}
			storage, err = NewFilesystemBeefStorage(expandedPath)
			if err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("unable to determine storage type from connection string: %s", connectionString)
		}

		storages = append(storages, storage)
	}

	if len(storages) == 0 {
		return nil, fmt.Errorf("no valid storage configurations provided")
	}

	return storages, nil
}
