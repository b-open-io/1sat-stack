package lockup

import (
	"bytes"
	"encoding/binary"
	"log"
	"math/big"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

type Lock struct {
	Address *script.Address `json:"address"`
	Until   uint32          `json:"until"`
}

func Decode(scr *script.Script, mainnet bool) *Lock {
	lockPrefixIndex := bytes.Index(*scr, LockPrefix)
	if lockPrefixIndex > -1 && bytes.Contains((*scr)[lockPrefixIndex:], LockSuffix) {
		lock := &Lock{}
		pos := lockPrefixIndex + len(LockPrefix)
		if op, err := scr.ReadOp(&pos); err != nil {
			log.Println(err)
		} else if len(op.Data) != 20 {
			return nil
		} else if lock.Address, err = script.NewAddressFromPublicKeyHash(op.Data, mainnet); err != nil {
			return nil
		}
		if op, err := scr.ReadOp(&pos); err != nil {
			log.Println(err)
		} else {
			until := make([]byte, 4)
			copy(until, op.Data)
			lock.Until = binary.LittleEndian.Uint32(until)
		}
		return lock
	}
	return nil
}

func (l Lock) Lock() *script.Script {
	s := script.NewFromBytes(LockPrefix)
	_ = s.AppendPushData(l.Address.PublicKeyHash)
	_ = s.AppendPushData((&interpreter.ScriptNumber{
		Val:          big.NewInt(int64(l.Until)),
		AfterGenesis: true,
	}).Bytes())
	return script.NewFromBytes(append(*s, LockSuffix...))
}

type LockUnlocker struct {
	PrivateKey  *ec.PrivateKey
	SigHashFlag *sighash.Flag
}

func (lu LockUnlocker) Sign(tx *transaction.Transaction, inputIndex uint32) (*script.Script, error) {
	if s, err := (&p2pkh.P2PKH{
		PrivateKey:  lu.PrivateKey,
		SigHashFlag: lu.SigHashFlag,
	}).Sign(tx, inputIndex); err != nil {
		return nil, err
	} else if preimage, err := tx.CalcInputPreimage(inputIndex, *lu.SigHashFlag); err != nil {
		return nil, err
	} else {
		_ = s.AppendPushData(preimage)
		return s, nil
	}
}

func (lu LockUnlocker) EstimateLength(tx *transaction.Transaction, inputIndex uint32) uint32 {
	if u, err := lu.Sign(tx, inputIndex); err != nil {
		return 0
	} else {
		return uint32(len(*u))
	}
}
