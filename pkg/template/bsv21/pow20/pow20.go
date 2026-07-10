package pow20

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

// Pow20 represents a POW20 token, extending BSV21 with POW20-specific fields
type Pow20 struct {
	// BSV21 base token data
	Bsv21         *bsv21.Bsv21 // Embed the BSV21 token data
	Txid          []byte       `json:"txid,omitempty"`
	Vout          uint32       `json:"vout,omitempty"`
	MaxSupply     uint64       `json:"maxSupply,omitempty"` // Max supply
	Reward        uint64       `json:"reward,omitempty"`    // Starting reward
	Difficulty    uint8        `json:"difficulty,omitempty"`
	Supply        uint64       `json:"supply,omitempty"` // Current supply
	LockingScript *script.Script
}

// Pow20Unlocker is a Pow20 with fields for unlocking
type Pow20Unlocker struct {
	Pow20                     // Embed Pow20
	Nonce     []byte          `json:"nonce,omitempty"`
	Recipient *script.Address `json:"recipient,omitempty"`
}

// Decode decodes a Pow20 token from a script
func Decode(s *script.Script) *Pow20 {
	if s == nil {
		return nil
	}

	// First try to decode as a BSV21 token
	bsv21Token := bsv21.Decode(s)
	if bsv21Token != nil && bsv21Token.Insc != nil && bsv21Token.Insc.File.Type == "application/bsv-20" {
		// Try to parse the JSON data
		var jsonData map[string]any
		if err := json.Unmarshal(bsv21Token.Insc.File.Content, &jsonData); err != nil {
			return nil
		}

		// Check if it's a POW20 token by looking for the contract field
		if contractType, ok := jsonData["contract"].(string); ok && contractType == "pow-20" {
			// This is a JSON-based POW20 token
			pow20 := &Pow20{
				Bsv21:         bsv21Token,
				LockingScript: s,
			}

			// Parse POW20-specific fields from the JSON
			if max, ok := jsonData["maxSupply"].(string); ok {
				maxVal, err := strconv.ParseUint(max, 10, 64)
				if err == nil {
					pow20.MaxSupply = maxVal
				}
			}

			if difficulty, ok := jsonData["difficulty"].(string); ok {
				diffVal, err := strconv.ParseUint(difficulty, 10, 8)
				if err == nil {
					pow20.Difficulty = uint8(diffVal)
				}
			}

			if reward, ok := jsonData["startingReward"].(string); ok {
				rewardVal, err := strconv.ParseUint(reward, 10, 64)
				if err == nil {
					pow20.Reward = rewardVal
				}
			}

			return pow20
		}
	}

	// Fall back to traditional script-based parsing for non-JSON POW20 tokens
	prefix := bytes.Index(*s, *pow20Prefix)
	if prefix == -1 {
		return nil
	}
	suffix := bytes.Index(*s, *pow20Suffix)
	if suffix == -1 {
		return nil
	}
	pos := prefix + len(*pow20Prefix)
	var err error
	var op *script.ScriptChunk

	p := &Pow20{
		LockingScript: s,
	}

	// Create a basic BSV21 token structure
	p.Bsv21 = &bsv21.Bsv21{}

	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	}
	symStr := string(op.Data)
	p.Bsv21.Symbol = &symStr

	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if number, err := interpreter.MakeScriptNumber(op.Data, len(op.Data), true, true); err != nil {
		return nil
	} else {
		p.MaxSupply = number.Val.Uint64()
	}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if op.Op >= script.Op1 && op.Op <= script.Op16 {
		dec := uint8(op.Op - 0x50)
		p.Bsv21.Decimals = &dec
	} else if len(op.Data) == 1 {
		dec := op.Data[0]
		p.Bsv21.Decimals = &dec
	}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if number, err := interpreter.MakeScriptNumber(op.Data, len(op.Data), true, true); err != nil {
		return nil
	} else {
		p.Reward = number.Val.Uint64()
	}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	}
	p.Difficulty = op.Op - 0x50

	pos = suffix + len(*pow20Suffix) + 2
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	}
	p.Bsv21.Id = string(op.Data)
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if number, err := interpreter.MakeScriptNumber(op.Data, len(op.Data), true, true); err != nil {
		return nil
	} else {
		p.Supply = number.Val.Uint64()
	}

	return p
}

func (p *Pow20) BuildUnlockTx(nonce []byte, recipient *script.Address, changeAddress *script.Address) (*transaction.Transaction, error) {
	tx := transaction.NewTransaction()
	unlock, err := p.Unlock(nonce, recipient)
	if err != nil {
		return nil, err
	}

	txid, _ := chainhash.NewHash(p.Txid)
	_ = tx.AddInputsFromUTXOs(&transaction.UTXO{
		TxID:                    txid,
		Vout:                    p.Vout,
		LockingScript:           p.LockingScript,
		Satoshis:                1,
		UnlockingScriptTemplate: unlock,
	})
	tx.Inputs[0].SequenceNumber = 0

	if p.Supply > p.Reward {
		restateScript := p.Lock(p.Supply - p.Reward)
		tx.AddOutput(&transaction.TransactionOutput{
			LockingScript: restateScript,
			Satoshis:      1,
		})
	}
	rewardScript := BuildInscription(p.Bsv21.Id, p.Reward)
	_ = rewardScript.AppendOpcodes(script.OpDUP, script.OpHASH160)
	_ = rewardScript.AppendPushData(recipient.PublicKeyHash)
	_ = rewardScript.AppendOpcodes(script.OpEQUALVERIFY, script.OpCHECKSIG)
	tx.AddOutput(&transaction.TransactionOutput{
		LockingScript: rewardScript,
		Satoshis:      1,
	})
	if changeAddress != nil {
		var change *transaction.TransactionOutput
		change = &transaction.TransactionOutput{
			Change: true,
		}
		change.LockingScript, _ = p2pkh.Lock(changeAddress)
		tx.AddOutput(change)
	}

	return tx, nil
}

func BuildInscription(id string, amt uint64) *script.Script {
	transferJSON := fmt.Sprintf(`{"p":"bsv-20","op":"transfer","id":"%s","amt":"%d"}`, id, amt)
	lockingScript := script.NewFromBytes([]byte{})
	_ = lockingScript.AppendOpcodes(script.OpFALSE, script.OpIF)
	_ = lockingScript.AppendPushData([]byte("ord"))
	_ = lockingScript.AppendOpcodes(script.Op1)
	_ = lockingScript.AppendPushData([]byte("application/bsv-20"))
	_ = lockingScript.AppendOpcodes(script.Op0)
	_ = lockingScript.AppendPushData([]byte(transferJSON))
	_ = lockingScript.AppendOpcodes(script.OpENDIF)
	return lockingScript
}

func (p *Pow20) Lock(supply uint64) *script.Script {
	s := BuildInscription(p.Bsv21.Id, supply)
	s = script.NewFromBytes(append(*s, *pow20Prefix...))
	symbolStr := ""
	if p.Bsv21 != nil && p.Bsv21.Symbol != nil {
		symbolStr = *p.Bsv21.Symbol
	}
	_ = s.AppendPushData([]byte(symbolStr))
	_ = s.AppendPushData(uint64ToBytes(p.MaxSupply))

	decimals := uint8(0)
	if p.Bsv21 != nil && p.Bsv21.Decimals != nil {
		decimals = *p.Bsv21.Decimals
	}

	if decimals <= 16 {
		_ = s.AppendOpcodes(byte(decimals + 0x50))
	} else {
		_ = s.AppendPushData([]byte{decimals})
	}
	_ = s.AppendPushData(uint64ToBytes(p.Reward))
	_ = s.AppendOpcodes(p.Difficulty + 0x50)
	s = script.NewFromBytes(append(*s, *pow20Suffix...))

	state := script.NewFromBytes([]byte{})
	_ = state.AppendOpcodes(script.OpRETURN, script.OpFALSE)
	_ = state.AppendPushData([]byte(p.Bsv21.Id))
	_ = state.AppendPushData(uint64ToBytes(supply))
	stateSize := uint32(len(*state) - 1)
	stateScript := binary.LittleEndian.AppendUint32(*state, stateSize)
	stateScript = append(stateScript, 0x00)

	lockingScript := make([]byte, len(*s)+len(stateScript))
	copy(lockingScript, *s)
	copy(lockingScript[len(*s):], stateScript)
	return script.NewFromBytes(lockingScript)
}

func (o *Pow20) Unlock(nonce []byte, recipient *script.Address) (*Pow20Unlocker, error) {
	unlock := &Pow20Unlocker{
		Pow20:     *o,
		Nonce:     nonce,
		Recipient: recipient,
	}
	return unlock, nil
}

func (p *Pow20Unlocker) Sign(tx *transaction.Transaction, inputIndex uint32) (*script.Script, error) {
	unlockScript := &script.Script{}

	// pow := o.Mine(o.Char)
	_ = unlockScript.AppendPushData(p.Recipient.PublicKeyHash)
	_ = unlockScript.AppendPushData([]byte(p.Nonce))
	if preimage, err := tx.CalcInputPreimage(inputIndex, sighash.All|sighash.AnyOneCanPayForkID); err != nil {
		return nil, err
	} else {
		_ = unlockScript.AppendPushData(preimage)
	}
	var change *transaction.TransactionOutput
	for _, output := range tx.Outputs {
		if output.Change {
			if change != nil {
				return nil, errors.New("multiple change outputs")
			}
			change = output
		}
	}
	if change != nil {
		_ = unlockScript.AppendPushData(uint64ToBytes(change.Satoshis))
		_ = unlockScript.AppendPushData((*change.LockingScript)[3:23])
	} else {
		_ = unlockScript.AppendOpcodes(script.Op0, script.Op0)
	}

	return unlockScript, nil
}

func (o *Pow20Unlocker) EstimateLength(tx *transaction.Transaction, inputIndex uint32) uint32 {
	noncePrefix, _ := script.PushDataPrefix(o.Nonce)
	preimage, _ := tx.CalcInputPreimage(inputIndex, sighash.AnyOneCanPayForkID|sighash.All)
	preimagePrefix, _ := script.PushDataPrefix(preimage)

	return uint32(55 + // OP_RETURN isGenesis push recipient push change sats push change pkh
		len(noncePrefix) + len(o.Nonce) + // push data ownerScript
		len(preimagePrefix) + len(preimage)) // push data preimage

}
