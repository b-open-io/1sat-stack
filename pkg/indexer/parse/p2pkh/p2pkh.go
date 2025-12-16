package p2pkh

import (
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/bsv-blockchain/go-sdk/script"
)

const P2PKH_TAG = "p2pkh"

type P2PKH struct {
	Address string `json:"address"`
}

type P2PKHIndexer struct {
	indexer.BaseIndexer
}

func (i *P2PKHIndexer) Tag() string {
	return P2PKH_TAG
}

func (i *P2PKHIndexer) Parse(idxCtx *indexer.IndexContext, vout uint32) any {
	txOutput := idxCtx.Tx.Outputs[vout]
	output := idxCtx.Outputs[vout]

	b := []byte(*txOutput.LockingScript)
	if len(b) >= 25 &&
		b[0] == script.OpDUP &&
		b[1] == script.OpHASH160 &&
		b[2] == script.OpDATA20 &&
		b[23] == script.OpEQUALVERIFY &&
		b[24] == script.OpCHECKSIG {

		if add, err := script.NewAddressFromPublicKeyHash(b[3:23], true); err == nil {
			output.AddOwnerFromAddress(add.AddressString)
			output.AddEvent(P2PKH_TAG + ":own:" + add.AddressString)
			return &P2PKH{
				Address: add.AddressString,
			}
		}
	}
	return nil
}
