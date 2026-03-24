package wasmparse

import (
	"fmt"

	"github.com/b-open-io/1sat-stack/proto/beefpb"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TransactionToAtomicBeef converts a *Transaction (with populated source
// transactions on inputs) to an AtomicBeef proto. Txids are pre-computed
// so the WASM module doesn't need to hash.
func TransactionToAtomicBeef(tx *transaction.Transaction) (*beefpb.AtomicBeef, error) {
	if tx == nil {
		return nil, fmt.Errorf("nil transaction")
	}

	txid := tx.TxID()
	seen := make(map[chainhash.Hash]bool)
	beef := &beefpb.Beef{}
	bumpMap := map[uint32]int{} // blockHeight → index in beef.Bumps

	if err := collectTransaction(tx, txid, beef, &bumpMap, seen); err != nil {
		return nil, err
	}

	return &beefpb.AtomicBeef{
		Txid: txid[:],
		Beef: beef,
	}, nil
}

func collectTransaction(
	tx *transaction.Transaction,
	txid *chainhash.Hash,
	beef *beefpb.Beef,
	bumpMap *map[uint32]int,
	seen map[chainhash.Hash]bool,
) error {
	if seen[*txid] {
		return nil
	}
	seen[*txid] = true

	// Collect ancestor transactions first
	for _, input := range tx.Inputs {
		if input.SourceTransaction != nil {
			srcTxid := input.SourceTransaction.TxID()
			if err := collectTransaction(input.SourceTransaction, srcTxid, beef, bumpMap, seen); err != nil {
				return err
			}
		}
	}

	rawBytes := tx.Bytes()

	beefTx := &beefpb.BeefTx{
		Txid:  txid[:],
		RawTx: rawBytes,
	}

	if tx.MerklePath != nil {
		idx, exists := (*bumpMap)[tx.MerklePath.BlockHeight]
		if !exists {
			idx = len(beef.Bumps)
			(*bumpMap)[tx.MerklePath.BlockHeight] = idx
			beef.Bumps = append(beef.Bumps, merklePathToProto(tx.MerklePath))
		}
		bumpIdx := uint32(idx)
		beefTx.BumpIndex = &bumpIdx
		beefTx.DataFormat = beefpb.DataFormat_RAW_TX_AND_BUMP_INDEX
	} else {
		beefTx.DataFormat = beefpb.DataFormat_RAW_TX
	}

	beef.Transactions = append(beef.Transactions, beefTx)
	return nil
}

func merklePathToProto(mp *transaction.MerklePath) *beefpb.MerklePath {
	pbPath := &beefpb.MerklePath{
		BlockHeight: mp.BlockHeight,
	}
	for _, level := range mp.Path {
		pbLevel := &beefpb.PathLevel{}
		for _, elem := range level {
			if elem == nil {
				continue
			}
			pbElem := &beefpb.PathElement{
				Offset: elem.Offset,
			}
			if elem.Hash != nil {
				pbElem.Hash = elem.Hash[:]
			}
			if elem.Txid != nil && *elem.Txid {
				pbElem.Txid = true
			}
			if elem.Duplicate != nil && *elem.Duplicate {
				pbElem.Duplicate = true
			}
			pbLevel.Elements = append(pbLevel.Elements, pbElem)
		}
		pbPath.Path = append(pbPath.Path, pbLevel)
	}
	return pbPath
}
