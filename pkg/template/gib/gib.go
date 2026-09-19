// Package gib decodes gib commit-head outputs.
//
// A commit head is a 1-satoshi PushDrop coin whose fields name a repository
// (its origin: the genesis directory outpoint), a branch, and the root
// directory outpoint that branch currently points at. The git commit object
// for that state rides the same output as an ordinal inscription. The coin's
// spend chain is the branch history: each push spends the previous head and
// creates the next one.
//
//	fields = ["gib", origin, branch, root, identity pubkey (, signature)]
//
// The decoder accepts outpoints as 36 raw bytes (txid || vout LE) or as a
// "txid_vout" / "txid.vout" string, and the identity as 33 raw bytes or 66
// hex characters. The PushDrop lock may sit before or after the fields and
// the inscription envelope may precede or follow the lock.
package gib

import (
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/pushdrop"
)

const (
	// ProtocolName is PushDrop field 0.
	ProtocolName = "gib"
	// EventName is the parser event emitted for every commit head. Per-repo
	// events are EventName + ":" + origin.
	EventName = "gib"

	FieldProtocol = 0
	FieldOrigin   = 1
	FieldBranch   = 2
	FieldRoot     = 3
	FieldIdentity = 4
	// FieldCount is the minimum field count. A sealed token carries one
	// trailing signature field, which is ignored.
	FieldCount = 5

	// MaxBranchBytes bounds the branch name (git refname component limit).
	MaxBranchBytes = 255
	// PubKeyLen is the compressed secp256k1 public-key size in bytes.
	PubKeyLen = 33
	// OutpointLen is the raw outpoint size in bytes.
	OutpointLen = 36
)

var (
	ErrNotOneSat   = errors.New("gib: commit head must carry exactly 1 satoshi")
	ErrNotPushDrop = errors.New("gib: locking script is not a PushDrop token")
	ErrFieldCount  = errors.New("gib: token needs at least 5 fields")
	ErrProtocol    = errors.New("gib: first field is not \"gib\"")
	ErrOrigin      = errors.New("gib: invalid origin field")
	ErrBranch      = errors.New("gib: invalid branch field")
	ErrRoot        = errors.New("gib: invalid root field")
	ErrIdentity    = errors.New("gib: invalid identity field")
)

// Head is a decoded commit head. Outpoints are in ordinal form (txid_vout)
// and keys are compressed hex, so the struct serializes directly.
type Head struct {
	Origin      string  `json:"origin"`
	Branch      string  `json:"branch"`
	Root        string  `json:"root"`
	Identity    string  `json:"identity"`
	LockingKey  string  `json:"lockingKey"`
	ContentType string  `json:"contentType,omitempty"`
	Commit      *Commit `json:"commit,omitempty"`
}

// Decode validates and decodes a gib commit head. It returns an error for
// any output that is not a well-formed head; callers treat that as "not gib".
func Decode(lockingScript *script.Script, satoshis uint64) (*Head, error) {
	if satoshis != 1 {
		return nil, ErrNotOneSat
	}
	if lockingScript == nil || len(*lockingScript) == 0 {
		return nil, ErrNotPushDrop
	}

	insc := inscription.Decode(lockingScript)
	candidates := []*script.Script{lockingScript}
	if insc != nil {
		candidates = candidates[:0]
		if len(insc.ScriptPrefix) > 0 {
			candidates = append(candidates, script.NewFromBytes(insc.ScriptPrefix))
		}
		if len(insc.ScriptSuffix) > 0 {
			candidates = append(candidates, script.NewFromBytes(insc.ScriptSuffix))
		}
	}

	var (
		lockKey *ec.PublicKey
		fields  [][]byte
	)
	for _, candidate := range candidates {
		if key, f, ok := decodePushDrop(candidate); ok {
			lockKey, fields = key, f
			break
		}
	}
	if lockKey == nil {
		return nil, ErrNotPushDrop
	}

	head, err := headFromFields(fields)
	if err != nil {
		return nil, err
	}
	head.LockingKey = hex.EncodeToString(lockKey.Compressed())

	if insc != nil && len(insc.File.Content) > 0 {
		head.ContentType = insc.File.Type
		if commit, err := ParseCommit(insc.File.Content); err == nil {
			head.Commit = commit
		}
	}
	return head, nil
}

// IsHead reports whether the output decodes as a commit head.
func IsHead(lockingScript *script.Script, satoshis uint64) bool {
	_, err := Decode(lockingScript, satoshis)
	return err == nil
}

func headFromFields(fields [][]byte) (*Head, error) {
	if len(fields) < FieldCount {
		return nil, ErrFieldCount
	}
	if string(fields[FieldProtocol]) != ProtocolName {
		return nil, ErrProtocol
	}
	origin, err := parseOutpointField(fields[FieldOrigin])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOrigin, err)
	}
	root, err := parseOutpointField(fields[FieldRoot])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRoot, err)
	}
	branch := fields[FieldBranch]
	if !validBranch(branch) {
		return nil, ErrBranch
	}
	identity, err := parsePubKeyField(fields[FieldIdentity])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdentity, err)
	}
	return &Head{
		Origin:   origin.OrdinalString(),
		Branch:   string(branch),
		Root:     root.OrdinalString(),
		Identity: hex.EncodeToString(identity.Compressed()),
	}, nil
}

func validBranch(b []byte) bool {
	if len(b) == 0 || len(b) > MaxBranchBytes || !utf8.Valid(b) {
		return false
	}
	for _, c := range b {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func parseOutpointField(b []byte) (*transaction.Outpoint, error) {
	if len(b) == OutpointLen {
		op := transaction.NewOutpointFromBytes(b)
		if op == nil {
			return nil, fmt.Errorf("bad raw outpoint")
		}
		return op, nil
	}
	if len(b) >= 66 && utf8.Valid(b) && (b[64] == '_' || b[64] == '.') {
		return transaction.OutpointFromString(string(b))
	}
	return nil, fmt.Errorf("expected 36 raw bytes or txid_vout, got %d bytes", len(b))
}

func parsePubKeyField(b []byte) (*ec.PublicKey, error) {
	raw := b
	if len(b) == PubKeyLen*2 {
		decoded, err := hex.DecodeString(string(b))
		if err != nil {
			return nil, err
		}
		raw = decoded
	}
	if len(raw) != PubKeyLen {
		return nil, fmt.Errorf("expected 33 raw bytes or 66 hex chars, got %d bytes", len(b))
	}
	return ec.PublicKeyFromBytes(raw)
}

// decodePushDrop returns the locking key and fields for a lock-before or
// lock-after PushDrop script.
func decodePushDrop(s *script.Script) (*ec.PublicKey, [][]byte, bool) {
	if pd := pushdrop.Decode(s); pd != nil && pd.LockingPublicKey != nil && len(pd.Fields) > 0 {
		return pd.LockingPublicKey, pd.Fields, true
	}
	return decodeLockAfter(s)
}

// decodeLockAfter parses "<fields...> <DROPs> <pubkey> OP_CHECKSIG".
func decodeLockAfter(s *script.Script) (*ec.PublicKey, [][]byte, bool) {
	chunks, err := s.Chunks()
	if err != nil || len(chunks) < 4 {
		return nil, nil, false
	}
	n := len(chunks)
	if chunks[n-1].Op != script.OpCHECKSIG {
		return nil, nil, false
	}
	keyChunk := chunks[n-2]
	if keyChunk == nil || len(keyChunk.Data) != PubKeyLen {
		return nil, nil, false
	}
	key, err := ec.PublicKeyFromBytes(keyChunk.Data)
	if err != nil {
		return nil, nil, false
	}

	i := n - 3
	dropped := 0
	for i >= 0 {
		switch chunks[i].Op {
		case script.OpDROP:
			dropped++
		case script.Op2DROP:
			dropped += 2
		default:
			goto done
		}
		i--
	}
done:
	fieldChunks := chunks[:i+1]
	if dropped == 0 || len(fieldChunks) != dropped {
		return nil, nil, false
	}
	fields := make([][]byte, 0, len(fieldChunks))
	for _, chunk := range fieldChunks {
		data, ok := pushOperand(chunk)
		if !ok {
			return nil, nil, false
		}
		fields = append(fields, data)
	}
	return key, fields, true
}

func pushOperand(chunk *script.ScriptChunk) ([]byte, bool) {
	if chunk == nil {
		return nil, false
	}
	switch {
	case chunk.Op == script.Op0:
		return []byte{}, true
	case chunk.Op == script.Op1NEGATE:
		return []byte{0x81}, true
	case chunk.Op >= script.Op1 && chunk.Op <= script.Op16:
		return []byte{chunk.Op - (script.Op1 - 1)}, true
	case chunk.Op >= script.OpDATA1 && chunk.Op <= script.OpPUSHDATA4:
		return append([]byte(nil), chunk.Data...), true
	default:
		return nil, false
	}
}
