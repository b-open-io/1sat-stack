package opns

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"

	hash "github.com/bsv-blockchain/go-sdk/primitives/hash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/bsv-blockchain/go-sdk/util"
)

const DIFFICULTY = 22

var txStr = "58b7558ea379f24266c7e2f5fe321992ad9a724fd7a87423ba412677179ccb25_0"
var GENESIS *transaction.Outpoint
var comp = big.NewInt(0)

func init() {
	var err error
	GENESIS, err = transaction.OutpointFromString(txStr)
	if err != nil {
		panic("Failed to parse genesis outpoint: " + err.Error())
	}
}

// type OpNS struct {
// 	Claimed []byte `json:"claimed,omitempty"`
// 	Domain  string `json:"domain"`
// 	PoW     []byte `json:"pow,omitempty"`
// }

// func Decode(scr *script.Script) *OpNS {
// 	if opNSPrefixIndex := bytes.Index(*scr, OpNSPrefix); opNSPrefixIndex == -1 {
// 		return nil
// 	} else if opNSSuffixIndex := bytes.Index(*scr, OpNSSuffix); opNSSuffixIndex == -1 {
// 		return nil
// 	} else {
// 		opNS := &OpNS{}
// 		stateScript := script.NewFromBytes((*scr)[opNSSuffixIndex+len(OpNSSuffix)+2:])
// 		pos := 0
// 		if genesisOp, err := stateScript.ReadOp(&pos); err != nil {
// 			return nil
// 		} else if genesisOp.Op != 36 {
// 			return nil
// 		} else if !overlay.NewOutpointFromTxBytes([36]byte(genesisOp.Data)).Equal(GENESIS) {
// 			return nil
// 		} else if claimedOp, err := stateScript.ReadOp(&pos); err != nil {
// 			return nil
// 		} else if domainOp, err := stateScript.ReadOp(&pos); err != nil {
// 			return nil
// 		} else if powOp, err := stateScript.ReadOp(&pos); err != nil {
// 			return nil
// 		} else {
// 			opNS.Claimed = claimedOp.Data
// 			opNS.Domain = string(domainOp.Data)
// 			opNS.PoW = powOp.Data
// 		}
// 		return opNS
// 	}
// }

type OpNS struct {
	Claimed       []byte         `json:"claimed"`
	Domain        string         `json:"domain"`
	Pow           []byte         `json:"pow"`
	LockingScript *script.Script `json:"lockingScript"`
	// SolutionHash  []byte         `json:"hash"`
}

type OpnsUnlocker struct {
	OpNS
	Char        byte           `json:"char"`
	OwnerScript *script.Script `json:"ownerScript"`
	Nonce       []byte         `json:"nonce"`
}

func Decode(s *script.Script) *OpNS {
	if !bytes.HasPrefix(*s, contract) {
		return nil
	}
	pos := len(contract) + 2

	o := &OpNS{}
	if opGenesis, err := s.ReadOp(&pos); err != nil {
		return nil
	} else if !bytes.Equal(opGenesis.Data, GENESIS.TxBytes()) {
		return nil
	} else if opClaimed, err := s.ReadOp(&pos); err != nil {
		return nil
	} else if opDomain, err := s.ReadOp(&pos); err != nil {
		return nil
	} else if opPow, err := s.ReadOp(&pos); err != nil {
		return nil
	} else {
		o.Claimed = opClaimed.Data
		o.Domain = string(opDomain.Data)
		o.Pow = opPow.Data
		o.LockingScript = s
	}
	return o
}

func Lock(claimed []byte, domain string, pow []byte) *script.Script {

	state := script.NewFromBytes([]byte{})
	state.AppendOpcodes(script.OpRETURN, script.OpFALSE)
	state.AppendPushData(GENESIS.TxBytes())
	state.AppendPushData(claimed)
	state.AppendPushData([]byte(domain))
	state.AppendPushData(pow)
	stateSize := uint32(len(*state) - 1)
	stateScript := binary.LittleEndian.AppendUint32(*state, stateSize)
	stateScript = append(stateScript, 0x00)

	s := make([]byte, len(contract)+len(stateScript))
	copy(s, contract)
	copy(s[len(contract):], stateScript)
	lockingScript := script.NewFromBytes(s)
	return lockingScript
}

func (o *OpNS) Unlock(char byte, nonce []byte, ownerScript *script.Script) (*OpnsUnlocker, error) {
	if !o.TestSolution(char, nonce) {
		return nil, errors.New("invalid nonce")
	}
	unlock := &OpnsUnlocker{
		OpNS:        *o,
		Char:        char,
		OwnerScript: ownerScript,
		Nonce:       nonce,
	}
	return unlock, nil
}

func (o *OpNS) TestSolution(char byte, nonce []byte) bool {
	test := make([]byte, 65)
	copy(test, o.Pow)
	test[32] = char
	copy(test[33:], nonce)
	hash := hash.Sha256d(test)
	testInt := new(big.Int).SetBytes(util.ReverseBytes(hash))
	testInt = testInt.Rsh(testInt, uint(256-DIFFICULTY))
	return testInt.Cmp(comp) == 0
}

func (o *OpnsUnlocker) Sign(tx *transaction.Transaction, inputIndex uint32) (*script.Script, error) {
	unlockScript := &script.Script{}

	unlockScript.AppendPushData([]byte{o.Char})
	unlockScript.AppendPushData([]byte(o.Nonce))
	unlockScript.AppendPushData(*o.OwnerScript)
	trailingOutputs := []byte{}
	if len(tx.Outputs) > 3 {
		for _, output := range tx.Outputs[3:] {
			trailingOutputs = append(trailingOutputs, output.Bytes()...)
		}
	}
	unlockScript.AppendPushData(trailingOutputs)
	if preimage, err := tx.CalcInputPreimage(inputIndex, sighash.All|sighash.AnyOneCanPayForkID); err != nil {
		return nil, err
	} else {
		unlockScript.AppendPushData(preimage)
	}
	return unlockScript, nil
}

func (o *OpnsUnlocker) EstimateLength(tx *transaction.Transaction, inputIndex uint32) uint32 {
	trailingOutputs := []byte{}
	if len(tx.Outputs) > 2 {
		for _, output := range tx.Outputs[2:] {
			trailingOutputs = append(trailingOutputs, output.Bytes()...)
		}
	}
	toPrefix, _ := script.PushDataPrefix(trailingOutputs)
	osPrefix, _ := script.PushDataPrefix(*o.OwnerScript)
	preimage, _ := tx.CalcInputPreimage(inputIndex, sighash.AnyOneCanPayForkID)
	preimagePrefix, _ := script.PushDataPrefix(preimage)

	return uint32(len(contract) +
		4 + // OP_RETURN isGenesis push char
		33 + // push data nonce
		len(osPrefix) + len(*o.OwnerScript) + // push data ownerScript
		len(toPrefix) + len(trailingOutputs) + // push data trailingOutputs
		len(preimagePrefix) + len(preimage)) // push data preimage

}
