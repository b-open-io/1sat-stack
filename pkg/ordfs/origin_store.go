package ordfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/dgraph-io/badger/v4"
)

var (
	prefixOrg = []byte("org:")
	prefixSeq = []byte("seq:")
	prefixRev = []byte("rev:")
	prefixMap = []byte("map:")
	prefixPar = []byte("par:")
)

const (
	outpointSize = 36
	orgValueSize = outpointSize + 4 // origin + seq
)

type BadgerOriginStore struct {
	db *badger.DB
}

func orgKey(outpoint *transaction.Outpoint) []byte {
	key := make([]byte, len(prefixOrg)+outpointSize)
	copy(key, prefixOrg)
	copy(key[len(prefixOrg):], outpoint.Bytes())
	return key
}

func encodeOrgValue(origin *transaction.Outpoint, seq uint32) []byte {
	val := make([]byte, orgValueSize)
	copy(val, origin.Bytes())
	binary.BigEndian.PutUint32(val[outpointSize:], seq)
	return val
}

func decodeOrgValue(val []byte) *OriginInfo {
	if len(val) < orgValueSize {
		return nil
	}
	return &OriginInfo{
		Origin: transaction.NewOutpointFromBytes(val[:outpointSize]),
		Seq:    binary.BigEndian.Uint32(val[outpointSize:]),
	}
}

func NewBadgerOriginStore(path string) (*BadgerOriginStore, error) {
	opts := badger.DefaultOptions(path)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open origin store: %w", err)
	}
	return &BadgerOriginStore{db: db}, nil
}

func seqKey(prefix []byte, origin *transaction.Outpoint, seq uint32) []byte {
	ob := origin.Bytes()
	key := make([]byte, len(prefix)+outpointSize+4)
	copy(key, prefix)
	copy(key[len(prefix):], ob)
	binary.BigEndian.PutUint32(key[len(prefix)+outpointSize:], seq)
	return key
}

// encodeRevValue encodes outpoint + contentLength + contentType for rev entries.
func encodeRevValue(outpoint *transaction.Outpoint, contentLength uint32, contentType string) []byte {
	ob := outpoint.Bytes()
	ct := []byte(contentType)
	val := make([]byte, outpointSize+4+len(ct))
	copy(val, ob)
	binary.BigEndian.PutUint32(val[outpointSize:], contentLength)
	copy(val[outpointSize+4:], ct)
	return val
}

// decodeRevValue decodes a rev entry value into a RevEntry.
func decodeRevValue(val []byte) *RevEntry {
	if len(val) < outpointSize+4 {
		return nil
	}
	return &RevEntry{
		Outpoint:      transaction.NewOutpointFromBytes(val[:outpointSize]),
		ContentLength: binary.BigEndian.Uint32(val[outpointSize : outpointSize+4]),
		ContentType:   string(val[outpointSize+4:]),
	}
}

func originPrefix(prefix []byte, origin *transaction.Outpoint) []byte {
	ob := origin.Bytes()
	key := make([]byte, len(prefix)+outpointSize)
	copy(key, prefix)
	copy(key[len(prefix):], ob)
	return key
}

func (s *BadgerOriginStore) GetOrigin(_ context.Context, outpoint *transaction.Outpoint) (*OriginInfo, error) {
	var result *OriginInfo
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(orgKey(outpoint))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			result = decodeOrgValue(val)
			if result == nil {
				return fmt.Errorf("invalid org value length %d", len(val))
			}
			return nil
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *BadgerOriginStore) GetSeqAt(_ context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	var result *transaction.Outpoint
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(seqKey(prefixSeq, origin, seq))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			result = transaction.NewOutpointFromBytes(val)
			return nil
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *BadgerOriginStore) GetLatestSeq(_ context.Context, origin *transaction.Outpoint) (*transaction.Outpoint, uint32, error) {
	var result *transaction.Outpoint
	var seq uint32
	prefix := originPrefix(prefixSeq, origin)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.PrefetchValues = true

		it := txn.NewIterator(opts)
		defer it.Close()

		seekKey := make([]byte, len(prefix)+1)
		copy(seekKey, prefix)
		seekKey[len(prefix)] = 0xff
		it.Seek(seekKey)

		if !it.ValidForPrefix(prefix) {
			return nil
		}

		item := it.Item()
		k := item.Key()
		seq = binary.BigEndian.Uint32(k[len(prefix):])
		return item.Value(func(val []byte) error {
			result = transaction.NewOutpointFromBytes(val)
			return nil
		})
	})
	if err != nil {
		return nil, 0, err
	}
	if result == nil {
		return nil, 0, nil
	}
	return result, seq, nil
}

func (s *BadgerOriginStore) getLatestBefore(prefix []byte, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	var result *transaction.Outpoint

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.PrefetchValues = true

		it := txn.NewIterator(opts)
		defer it.Close()

		it.Seek(seqKey(prefix, origin, seq+1))

		pfx := originPrefix(prefix, origin)
		if !it.ValidForPrefix(pfx) {
			return nil
		}

		item := it.Item()
		return item.Value(func(val []byte) error {
			result = transaction.NewOutpointFromBytes(val)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *BadgerOriginStore) GetLatestRevBefore(_ context.Context, origin *transaction.Outpoint, seq uint32) (*RevEntry, error) {
	var result *RevEntry

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.PrefetchValues = true

		it := txn.NewIterator(opts)
		defer it.Close()

		it.Seek(seqKey(prefixRev, origin, seq+1))

		pfx := originPrefix(prefixRev, origin)
		if !it.ValidForPrefix(pfx) {
			return nil
		}

		return it.Item().Value(func(val []byte) error {
			result = decodeRevValue(val)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *BadgerOriginStore) GetLatestMapBefore(_ context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	return s.getLatestBefore(prefixMap, origin, seq)
}

func (s *BadgerOriginStore) GetLatestParentBefore(_ context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	return s.getLatestBefore(prefixPar, origin, seq)
}

func (s *BadgerOriginStore) GetAllMapUpTo(_ context.Context, origin *transaction.Outpoint, seq uint32) ([]*transaction.Outpoint, error) {
	var results []*transaction.Outpoint
	prefix := originPrefix(prefixMap, origin)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.Key()
			entrySeq := binary.BigEndian.Uint32(k[len(prefix):])
			if entrySeq > seq {
				break
			}
			if err := item.Value(func(val []byte) error {
				results = append(results, transaction.NewOutpointFromBytes(val))
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *BadgerOriginStore) GetMapSeq(_ context.Context, origin *transaction.Outpoint, outpoint *transaction.Outpoint) (uint32, error) {
	var seq uint32
	var found bool
	prefix := originPrefix(prefixMap, origin)
	target := outpoint.Bytes()

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			if err := item.Value(func(val []byte) error {
				if bytes.Equal(val, target) {
					k := item.Key()
					seq = binary.BigEndian.Uint32(k[len(prefix):])
					found = true
				}
				return nil
			}); err != nil {
				return err
			}
			if found {
				break
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("map entry not found for outpoint")
	}
	return seq, nil
}

func (s *BadgerOriginStore) WriteBatch(_ context.Context, batch *OriginBatch) error {
	wb := s.db.NewWriteBatch()
	defer wb.Cancel()

	for _, entry := range batch.Entries {
		opBytes := entry.Outpoint.Bytes()

		if err := wb.Set(orgKey(entry.Outpoint), encodeOrgValue(batch.Origin, entry.Seq)); err != nil {
			return fmt.Errorf("failed to write origin mapping: %w", err)
		}
		if err := wb.Set(seqKey(prefixSeq, batch.Origin, entry.Seq), opBytes); err != nil {
			return fmt.Errorf("failed to write seq entry: %w", err)
		}
		if entry.HasRev {
			revVal := encodeRevValue(entry.Outpoint, entry.ContentLength, entry.ContentType)
			if err := wb.Set(seqKey(prefixRev, batch.Origin, entry.Seq), revVal); err != nil {
				return fmt.Errorf("failed to write rev entry: %w", err)
			}
		}
		if entry.HasMap {
			if err := wb.Set(seqKey(prefixMap, batch.Origin, entry.Seq), opBytes); err != nil {
				return fmt.Errorf("failed to write map entry: %w", err)
			}
		}
		if entry.HasPar {
			if err := wb.Set(seqKey(prefixPar, batch.Origin, entry.Seq), opBytes); err != nil {
				return fmt.Errorf("failed to write par entry: %w", err)
			}
		}
	}

	return wb.Flush()
}

func (s *BadgerOriginStore) AddEntry(_ context.Context, origin *transaction.Outpoint, entry *OriginEntry) error {
	return s.db.Update(func(txn *badger.Txn) error {
		opBytes := entry.Outpoint.Bytes()

		if err := txn.Set(orgKey(entry.Outpoint), encodeOrgValue(origin, entry.Seq)); err != nil {
			return err
		}
		if err := txn.Set(seqKey(prefixSeq, origin, entry.Seq), opBytes); err != nil {
			return err
		}
		if entry.HasRev {
			revVal := encodeRevValue(entry.Outpoint, entry.ContentLength, entry.ContentType)
			if err := txn.Set(seqKey(prefixRev, origin, entry.Seq), revVal); err != nil {
				return err
			}
		}
		if entry.HasMap {
			if err := txn.Set(seqKey(prefixMap, origin, entry.Seq), opBytes); err != nil {
				return err
			}
		}
		if entry.HasPar {
			if err := txn.Set(seqKey(prefixPar, origin, entry.Seq), opBytes); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerOriginStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
