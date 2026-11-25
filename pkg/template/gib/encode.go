package gib

import (
	"fmt"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Fields builds the PushDrop fields for a commit head. Outpoints are
// written as txid_vout strings and the identity as raw compressed bytes.
// branchedFrom is empty on an ordinary push; it is set on a branch's first
// head and on a merge. An empty branched-from field is an empty push, which
// PushDrop encodes minimally as OP_FALSE.
func Fields(origin, branch, root string, identity *ec.PublicKey, branchedFrom string) ([][]byte, error) {
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
	from := []byte{}
	if branchedFrom != "" {
		if _, err := transaction.OutpointFromString(branchedFrom); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBranchedFrom, err)
		}
		from = []byte(branchedFrom)
	}
	return [][]byte{
		[]byte(ProtocolName),
		[]byte(origin),
		[]byte(branch),
		[]byte(root),
		identity.Compressed(),
		from,
	}, nil
}

// LockingScript builds a lock-before PushDrop script:
//
//	<lockKey> OP_CHECKSIG <field>... OP_2DROP... [OP_DROP]
//
// Nothing is inscribed on a head: the commit objects live in the published
// root's `.git` directory, not on this output. It is the reference shape
// the SDK publishes; Decode also accepts the lock-after variant.
func LockingScript(lockKey *ec.PublicKey, fields [][]byte) (*script.Script, error) {
	if lockKey == nil {
		return nil, fmt.Errorf("gib: locking key is required")
	}
	if len(fields) == 0 {
		return nil, ErrFieldCount
	}
	s := &script.Script{}
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
