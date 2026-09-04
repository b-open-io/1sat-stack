package ecosystemalias

import (
	"fmt"
	"math/big"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
)

const (
	// CodeInvalidScript identifies a malformed BRC-48 lock-after script.
	CodeInvalidScript Code = "invalid-script"
)

// Decode validates and decodes a BRC-169 BRC-48 lock-after output.
//
// The accepted script is exactly:
//
//	<field 1> ... <field 6> OP_2DROP OP_2DROP OP_2DROP <owner key> OP_CHECKSIG
//
// The owner key controls spending the output. It is independent of the
// certifier key in field 5, which verifies the signature in field 6.
func Decode(lockingScript *script.Script, satoshis uint64) (*Claim, error) {
	if err := ValidateSats(satoshis); err != nil {
		return nil, err
	}

	fields, err := decodeLockAfter(lockingScript)
	if err != nil {
		return nil, err
	}

	claim, err := ValidateTokenFields(fields)
	if err != nil {
		return nil, err
	}

	certifier, err := ec.ParsePubKey(claim.CertifierKey[:])
	if err != nil {
		return nil, fail(CodeInvalidCertifier, "certifier key must encode a valid secp256k1 curve point")
	}
	signature, err := ec.FromDER(claim.Signature)
	if err != nil {
		return nil, fail(CodeInvalidSignature, "signature must be DER-encoded ECDSA")
	}

	// Digest performs the one and only SHA-256 over fields 1-5. Signature
	// verification consumes that digest directly; it must not hash it again.
	digest := Digest(claim.Alias, claim.Domain, claim.CertifierKey)
	if !signature.Verify(digest[:], certifier) {
		return nil, fail(CodeInvalidSignature, "signature does not verify against the certifier key")
	}

	return claim, nil
}

func decodeLockAfter(lockingScript *script.Script) ([][]byte, error) {
	if lockingScript == nil {
		return nil, fail(CodeInvalidScript, "locking script is nil")
	}
	chunks, err := lockingScript.Chunks()
	if err != nil {
		return nil, fail(CodeInvalidScript, fmt.Sprintf("locking script cannot be decoded: %v", err))
	}
	if len(chunks) < 2 || chunks[len(chunks)-1].Op != script.OpCHECKSIG {
		return nil, fail(CodeInvalidScript, "locking script must end with an owner key and OP_CHECKSIG")
	}

	ownerChunk := chunks[len(chunks)-2]
	ownerKey, ok := pushOperand(ownerChunk)
	if !ok || len(ownerKey) != CertifierKeyLen {
		return nil, fail(CodeInvalidScript, "owner key must be a pushed 33-byte compressed public key")
	}
	if ownerKey[0] != 0x02 && ownerKey[0] != 0x03 {
		return nil, fail(CodeInvalidScript, "owner key must be a compressed secp256k1 public key")
	}
	if new(big.Int).SetBytes(ownerKey[1:]).Cmp(ec.S256().Params().P) >= 0 {
		return nil, fail(CodeInvalidScript, "owner key x-coordinate must be within the secp256k1 field")
	}
	if _, err := ec.ParsePubKey(ownerKey); err != nil {
		return nil, fail(CodeInvalidScript, "owner key must encode a valid secp256k1 curve point")
	}

	dropEnd := len(chunks) - 2
	dropStart := dropEnd
	for dropStart > 0 && chunks[dropStart-1].Op == script.Op2DROP {
		dropStart--
	}
	if dropEnd-dropStart != FieldCount/2 {
		return nil, fail(CodeInvalidScript, "locking script must contain exactly three OP_2DROP opcodes before the owner key")
	}
	if dropStart != FieldCount {
		return nil, fail(CodeFieldCount, fmt.Sprintf("token must have exactly %d fields, got %d", FieldCount, dropStart))
	}

	fields := make([][]byte, FieldCount)
	for i, chunk := range chunks[:dropStart] {
		field, ok := pushOperand(chunk)
		if !ok {
			return nil, fail(CodeInvalidScript, fmt.Sprintf("field %d must be one push operand", i+1))
		}
		fields[i] = field
	}
	return fields, nil
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
