package beef

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestSingleTxBeefCarriesProof(t *testing.T) {
	tx := transaction.NewTransaction()
	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      1,
		LockingScript: &script.Script{},
	})
	txid := tx.TxID()
	isTxid := true
	tx.MerklePath = &transaction.MerklePath{
		BlockHeight: 800000,
		Path: [][]*transaction.PathElement{{
			{Offset: 0, Hash: txid, Txid: &isTxid},
		}},
	}

	raw, err := singleTxBeef(txid, tx)
	if err != nil {
		t.Fatal(err)
	}
	parsed, got, gotID, err := transaction.ParseBeef(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || gotID == nil || !gotID.IsEqual(txid) {
		t.Fatalf("parsed txid %v", gotID)
	}
	if got.MerklePath == nil || got.MerklePath.BlockHeight != 800000 {
		t.Fatalf("proof not preserved: %+v", got.MerklePath)
	}
	if parsed == nil {
		t.Fatal("expected beef")
	}
}
