package ltm

import (
	"bytes"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
)

type LockToMint struct {
	Symbol       string
	Max          uint64
	Decimals     uint8
	Multiplier   uint64
	LockDuration uint64
	StartHeight  uint64
}

func Decode(s *script.Script) *LockToMint {
	prefix := bytes.Index(*s, *ltmPrefix)
	if prefix == -1 {
		return nil
	}
	suffix := bytes.Index(*s, *ltmSuffix)
	if suffix == -1 {
		return nil
	}
	pos := prefix + len(*ltmPrefix)
	var err error
	var op *script.ScriptChunk

	ltm := &LockToMint{}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	}
	ltm.Symbol = string(op.Data)
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if number, err := interpreter.MakeScriptNumber(op.Data, len(op.Data), true, true); err != nil {
		return nil
	} else {
		ltm.Max = number.Val.Uint64()
	}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	}
	if op.Op >= script.Op1 && op.Op <= script.Op16 {
		ltm.Decimals = op.Op - 0x50
	} else if len(op.Data) == 1 {
		ltm.Decimals = op.Data[0]
	}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if number, err := interpreter.MakeScriptNumber(op.Data, len(op.Data), true, true); err != nil {
		return nil
	} else {
		ltm.Multiplier = number.Val.Uint64()
	}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if number, err := interpreter.MakeScriptNumber(op.Data, len(op.Data), true, true); err != nil {
		return nil
	} else {
		ltm.LockDuration = number.Val.Uint64()
	}
	if op, err = s.ReadOp(&pos); err != nil {
		return nil
	} else if number, err := interpreter.MakeScriptNumber(op.Data, len(op.Data), true, true); err != nil {
		return nil
	} else {
		ltm.StartHeight = number.Val.Uint64()
	}
	return ltm
}
