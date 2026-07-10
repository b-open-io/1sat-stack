package cosign

import (
	"encoding/hex"
	"errors"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
)

var (
	ErrBadPublicKeyHash = errors.New("invalid public key hash")
	ErrNoPrivateKey     = errors.New("private key not supplied")
)

type Cosign struct {
	Address  string `json:"address"`
	Cosigner string `json:"cosigner"`
}

func Decode(s *script.Script) *Cosign {
	chunks, _ := s.Chunks()
	for i := range len(chunks) - 6 {
		if chunks[0+i].Op == script.OpDUP &&
			chunks[1+i].Op == script.OpHASH160 &&
			len(chunks[2+i].Data) == 20 &&
			chunks[3+i].Op == script.OpEQUALVERIFY &&
			chunks[4+i].Op == script.OpCHECKSIGVERIFY &&
			len(chunks[5+i].Data) == 33 &&
			chunks[6+i].Op == script.OpCHECKSIG {

			cosign := &Cosign{
				Cosigner: hex.EncodeToString(chunks[5+i].Data),
			}
			if add, err := script.NewAddressFromPublicKeyHash(chunks[2+i].Data, true); err == nil {
				cosign.Address = add.AddressString
			}
			return cosign
		}
	}
	return nil
}

func Lock(a *script.Address, pubkey *ec.PublicKey) (*script.Script, error) {
	if len(a.PublicKeyHash) != 20 {
		return nil, ErrBadPublicKeyHash
	}
	scr := script.Script(make([]byte, 0, 59))
	s := &scr
	_ = s.AppendOpcodes(script.OpDUP, script.OpHASH160)
	_ = s.AppendPushData(a.PublicKeyHash)
	_ = s.AppendOpcodes(script.OpEQUALVERIFY, script.OpCHECKSIGVERIFY)
	_ = s.AppendPushData(pubkey.Compressed())
	_ = s.AppendOpcodes(script.OpCHECKSIG)
	return s, nil
}

func OwnerUnlock(key *ec.PrivateKey, sigHashFlag *sighash.Flag) (*CosignOwnerTemplate, error) {
	if key == nil {
		return nil, ErrNoPrivateKey
	}
	if sigHashFlag == nil {
		shf := sighash.AllForkID
		sigHashFlag = &shf
	}
	return &CosignOwnerTemplate{
		PrivateKey:  key,
		SigHashFlag: sigHashFlag,
	}, nil
}

type CosignOwnerTemplate struct {
	PrivateKey  *ec.PrivateKey
	SigHashFlag *sighash.Flag
}

func (c *CosignOwnerTemplate) Sign(tx *transaction.Transaction, inputIndex uint32) (*script.Script, error) {
	if tx.Inputs[inputIndex].SourceTxOutput() == nil {
		return nil, transaction.ErrEmptyPreviousTx
	}

	sh, err := tx.CalcInputSignatureHash(inputIndex, *c.SigHashFlag)
	if err != nil {
		return nil, err
	}

	sig, err := c.PrivateKey.Sign(sh)
	if err != nil {
		return nil, err
	}

	pubKey := c.PrivateKey.PubKey().Compressed()
	signature := sig.Serialize()

	sigBuf := make([]byte, 0)
	sigBuf = append(sigBuf, signature...)
	sigBuf = append(sigBuf, uint8(*c.SigHashFlag))

	s := &script.Script{}
	if err = s.AppendPushData(sigBuf); err != nil {
		return nil, err
	} else if err = s.AppendPushData(pubKey); err != nil {
		return nil, err
	}

	return s, nil
}

func (c *CosignOwnerTemplate) EstimateLength(_ *transaction.Transaction, inputIndex uint32) uint32 {
	return 185
}

type CosignApproverTemplate struct {
	PrivateKey  *ec.PrivateKey
	SigHashFlag *sighash.Flag
	UserScript  *script.Script
}

func ApproverUnlock(key *ec.PrivateKey, userScript *script.Script, sigHashFlag *sighash.Flag) (*CosignApproverTemplate, error) {
	if key == nil {
		return nil, ErrNoPrivateKey
	}
	if sigHashFlag == nil {
		shf := sighash.AllForkID
		sigHashFlag = &shf
	}
	return &CosignApproverTemplate{
		PrivateKey:  key,
		SigHashFlag: sigHashFlag,
		UserScript:  userScript,
	}, nil
}

func (c *CosignApproverTemplate) Sign(tx *transaction.Transaction, inputIndex uint32) (*script.Script, error) {
	if tx.Inputs[inputIndex].SourceTxOutput() == nil {
		return nil, transaction.ErrEmptyPreviousTx
	}

	sh, err := tx.CalcInputSignatureHash(inputIndex, *c.SigHashFlag)
	if err != nil {
		return nil, err
	}

	sig, err := c.PrivateKey.Sign(sh)
	if err != nil {
		return nil, err
	}

	signature := sig.Serialize()

	sigBuf := make([]byte, 0)
	sigBuf = append(sigBuf, signature...)
	sigBuf = append(sigBuf, uint8(*c.SigHashFlag))

	s := &script.Script{}
	chunks, _ := c.UserScript.Chunks()
	if err = s.AppendPushData(sigBuf); err != nil {
		return nil, err
	}
	for _, op := range chunks {
		if err = s.AppendPushData(op.Data); err != nil {
			return nil, err
		}
	}

	return s, nil
}

func (c *CosignApproverTemplate) EstimateLength(_ *transaction.Transaction, inputIndex uint32) uint32 {
	return 185
}
