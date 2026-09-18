package gib

import (
	"fmt"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Fields builds the PushDrop fields for a commit head. Outpoints are
// written as txid_vout strings and the identity as raw compressed bytes.
func Fields(origin, branch, root string, identity *ec.PublicKey) ([][]byte, error) {
	if _, err := transaction.OutpointFromString(origin); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOrigin, err)
	}
	if _, err := transaction.OutpointFromString(root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRoot, err)
	}
	if !validBranch([]byte(branch)) {
		return nil, ErrBranch
	}
	if identity == nil {
		return nil, ErrIdentity
	}
	return [][]byte{
		[]byte(ProtocolName),
		[]byte(origin),
		[]byte(branch),
		[]byte(root),
		identity.Compressed(),
	}, nil
}

// LockingScript builds a lock-before PushDrop script, optionally prefixed
// with an ordinal inscription envelope carrying the git commit object:
//
//	[OP_FALSE OP_IF "ord" OP_1 <contentType> OP_0 <commit> OP_ENDIF]
//	<lockKey> OP_CHECKSIG <field>... OP_2DROP... [OP_DROP]
//
// It is the reference shape the SDK publishes; Decode also accepts the
// lock-after and inscription-suffix variants.
func LockingScript(lockKey *ec.PublicKey, fields [][]byte, commit []byte, contentType string) (*script.Script, error) {
	if lockKey == nil {
		return nil, fmt.Errorf("gib: locking key is required")
	}
	if len(fields) == 0 {
		return nil, ErrFieldCount
	}
	s := &script.Script{}
	if len(commit) > 0 {
		if err := s.AppendOpcodes(script.OpFALSE, script.OpIF); err != nil {
			return nil, err
		}
		if err := s.AppendPushData([]byte("ord")); err != nil {
			return nil, err
		}
		if err := s.AppendOpcodes(script.Op1); err != nil {
			return nil, err
		}
		if err := s.AppendPushData([]byte(contentType)); err != nil {
			return nil, err
		}
		if err := s.AppendOpcodes(script.Op0); err != nil {
			return nil, err
		}
		if err := s.AppendPushData(commit); err != nil {
			return nil, err
		}
		if err := s.AppendOpcodes(script.OpENDIF); err != nil {
			return nil, err
		}
	}
	if err := s.AppendPushData(lockKey.Compressed()); err != nil {
		return nil, err
	}
	if err := s.AppendOpcodes(script.OpCHECKSIG); err != nil {
		return nil, err
	}
	for _, field := range fields {
		if err := s.AppendPushData(field); err != nil {
			return nil, err
		}
	}
	for i := 0; i < len(fields)/2; i++ {
		if err := s.AppendOpcodes(script.Op2DROP); err != nil {
			return nil, err
		}
	}
	if len(fields)%2 == 1 {
		if err := s.AppendOpcodes(script.OpDROP); err != nil {
			return nil, err
		}
	}
	return s, nil
}
