package ordlock

import (
	"bytes"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type OrdLock struct {
	Seller   *script.Address `json:"seller"`
	Price    uint64          `json:"price"`
	PricePer float64         `json:"pricePer"`
	PayOut   []byte          `json:"payout"`
}

func Decode(scr *script.Script) *OrdLock {
	if sCryptPrefixIndex := bytes.Index(*scr, OrdLockPrefix); sCryptPrefixIndex == -1 {
		return nil
	} else if ordLockSuffixIndex := bytes.Index(*scr, OrdLockSuffix); ordLockSuffixIndex == -1 {
		return nil
	} else if ordLockOps, err := script.DecodeScript((*scr)[sCryptPrefixIndex+len(OrdLockPrefix) : ordLockSuffixIndex]); err != nil || len(ordLockOps) == 0 {
		return nil
	} else {
		// pkhash := lib.PKHash(ordLockOps[0].Data)
		payOutput := &transaction.TransactionOutput{}
		if _, err = payOutput.ReadFrom(bytes.NewReader(ordLockOps[1].Data)); err != nil {
			return nil
		}
		ordLock := &OrdLock{
			Price:  payOutput.Satoshis,
			PayOut: payOutput.Bytes(),
		}
		if ordLock.Seller, err = script.NewAddressFromPublicKeyHash(ordLockOps[0].Data, true); err != nil {
			return nil
		}

		return ordLock
	}
}
