package beef

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// slowMemStorage holds BEEFs in memory and delays reads so concurrent loads of
// the same txid overlap in the dedup loader.
type slowMemStorage struct {
	mu    sync.Mutex
	beefs map[chainhash.Hash][]byte
}

func (m *slowMemStorage) Get(_ context.Context, txid *chainhash.Hash) ([]byte, error) {
	time.Sleep(5 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.beefs[*txid]; ok {
		return b, nil
	}
	return nil, ErrNotFound
}

func (m *slowMemStorage) Put(_ context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beefs[*txid] = beefBytes
	return nil
}

func (m *slowMemStorage) UpdateMerklePath(context.Context, *chainhash.Hash) ([]byte, error) {
	return nil, ErrNotFound
}

func (m *slowMemStorage) GetRawTx(context.Context, *chainhash.Hash) ([]byte, error) {
	return nil, ErrNotFound
}

func (m *slowMemStorage) GetProof(context.Context, *chainhash.Hash) ([]byte, error) {
	return nil, ErrNotFound
}

func (m *slowMemStorage) Close() error { return nil }

// TestBEEFFromSharedAncestorsConcurrent mirrors the broadcast handler's raw-tx
// path: many concurrent PopulateAncestors + Transaction.BEEF calls over
// ancestors mined in the same block. Transaction.BEEF combines BUMPs in place,
// so each caller must get its own ancestor proofs; otherwise proofs from other
// requests leak into (and race on) every BEEF built.
func TestBEEFFromSharedAncestorsConcurrent(t *testing.T) {
	const height = 100

	ancestors := make([]*transaction.Transaction, 4)
	leaves := make([]*chainhash.Hash, 4)
	for i := range ancestors {
		tx := transaction.NewTransaction()
		tx.AddInput(&transaction.TransactionInput{
			SourceTXID:     &chainhash.Hash{byte(i + 1)},
			SequenceNumber: 0xffffffff,
		})
		lockingScript := script.NewFromBytes([]byte{script.OpFALSE, script.OpRETURN, script.OpDATA1, byte(i)})
		tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: lockingScript})
		ancestors[i] = tx
		leaves[i] = tx.TxID()
	}
	parents := []*chainhash.Hash{
		transaction.MerkleTreeParent(leaves[0], leaves[1]),
		transaction.MerkleTreeParent(leaves[2], leaves[3]),
	}

	store := &slowMemStorage{beefs: map[chainhash.Hash][]byte{}}
	isTxid := true
	for i, tx := range ancestors {
		tx.MerklePath = transaction.NewMerklePath(height, [][]*transaction.PathElement{
			{
				{Offset: uint64(i), Hash: leaves[i], Txid: &isTxid},
				{Offset: uint64(i ^ 1), Hash: leaves[i^1]},
			},
			{{Offset: uint64((i >> 1) ^ 1), Hash: parents[(i>>1)^1]}},
		})
		beefBytes, err := tx.BEEF()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Put(context.Background(), leaves[i], beefBytes); err != nil {
			t.Fatal(err)
		}
	}

	s := NewStorageFromProviders([]BaseBeefStorage{store}, nil)

	var pairs [][2]int
	for i := range ancestors {
		for j := i + 1; j < len(ancestors); j++ {
			pairs = append(pairs, [2]int{i, j})
		}
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		for _, p := range pairs {
			wg.Add(1)
			go func(p [2]int) {
				defer wg.Done()
				child := transaction.NewTransaction()
				for _, i := range p {
					child.AddInput(&transaction.TransactionInput{
						SourceTXID:     leaves[i],
						SequenceNumber: 0xffffffff,
					})
				}
				<-start
				if err := s.PopulateAncestors(context.Background(), child); err != nil {
					t.Error(err)
					return
				}
				beefBytes, err := child.BEEF()
				if err != nil {
					t.Error(err)
					return
				}
				beef, _, _, err := transaction.ParseBeef(beefBytes)
				if err != nil {
					t.Error(err)
					return
				}
				if len(beef.BUMPs) != 1 {
					t.Errorf("inputs %v: expected 1 BUMP, got %d", p, len(beef.BUMPs))
					return
				}
				want := map[uint64]bool{}
				for _, i := range p {
					want[uint64(i)] = true
					want[uint64(i^1)] = true
				}
				got := map[uint64]bool{}
				for _, leaf := range beef.BUMPs[0].Path[0] {
					got[leaf.Offset] = true
				}
				if len(got) != len(want) {
					t.Errorf("inputs %v: BUMP level 0 has offsets %v, want %v", p, got, want)
				}
			}(p)
		}
	}
	close(start)
	wg.Wait()
}
