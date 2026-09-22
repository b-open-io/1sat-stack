package beef

import (
	"context"
	"errors"

	"github.com/b-open-io/go-junglebus"
	"github.com/b-open-io/go-junglebus/transports"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type JunglebusBeefStorage struct {
	client *junglebus.Client
}

// NewJunglebusBeefStorageWithClient creates a JungleBus storage using the provided client.
func NewJunglebusBeefStorageWithClient(client *junglebus.Client) *JunglebusBeefStorage {
	return &JunglebusBeefStorage{
		client: client,
	}
}

func (t *JunglebusBeefStorage) Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	if t.client == nil {
		return nil, ErrNotFound
	}

	beefBytes, err := t.client.GetBeef(ctx, txid.String())
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(beefBytes) == 0 {
		return nil, ErrNotFound
	}
	// Do not parse here. The caller parses once. A validity check would
	// build and discard a full transaction graph for every download.
	return beefBytes, nil
}

func (t *JunglebusBeefStorage) Put(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	return nil // JungleBus is read-only
}

// UpdateMerklePath builds a single-transaction BEEF from the raw transaction
// and the JungleBus proof route. It does not download /transaction/beef.
func (t *JunglebusBeefStorage) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	if t.client == nil {
		return nil, ErrNotFound
	}
	id := txid.String()
	raw, err := t.client.GetRawTransaction(ctx, id)
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	proof, err := t.client.GetProof(ctx, id)
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	tx, err := transaction.NewTransactionFromBytes(raw)
	if err != nil {
		return nil, err
	}
	path, err := transaction.NewMerklePathFromBinary(proof)
	if err != nil {
		return nil, err
	}
	tx.MerklePath = path
	return singleTxBeef(txid, tx)
}

func singleTxBeef(txid *chainhash.Hash, tx *transaction.Transaction) ([]byte, error) {
	beef := &transaction.Beef{
		Version:      transaction.BEEF_V2,
		BUMPs:        []*transaction.MerklePath{},
		Transactions: map[chainhash.Hash]*transaction.BeefTx{},
	}
	beefTx := &transaction.BeefTx{
		Transaction: tx,
		BumpIndex:   -1,
		DataFormat:  transaction.RawTx,
	}
	if tx.MerklePath != nil {
		beef.BUMPs = append(beef.BUMPs, tx.MerklePath)
		beefTx.BumpIndex = 0
		beefTx.DataFormat = transaction.RawTxAndBumpIndex
	}
	beef.Transactions[*txid] = beefTx
	return beef.AtomicBytes(txid)
}

func (t *JunglebusBeefStorage) GetRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	if t.client == nil {
		return nil, ErrNotFound
	}

	rawTx, err := t.client.GetRawTransaction(ctx, txid.String())
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return rawTx, nil
}

func (t *JunglebusBeefStorage) GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	if t.client == nil {
		return nil, ErrNotFound
	}

	proof, err := t.client.GetProof(ctx, txid.String())
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return proof, nil
}

func (j *JunglebusBeefStorage) Close() error {
	return nil
}
