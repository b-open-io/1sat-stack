package ecosystemalias

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
)

func TestDecodeValidVectors(t *testing.T) {
	doc, _ := loadFixture(t)
	otherOwner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()

	for i, item := range fixtureSlice(t, doc, "tokens", "positive") {
		row := item.(map[string]any)
		name := asString(t, row["name"])
		fields := decodeFixtureFields(t, row)
		owner := fields[FieldCertifier]
		if i != 0 {
			owner = otherOwner
		}

		t.Run(name, func(t *testing.T) {
			claim, err := Decode(decodeBuildLockAfter(t, fields, owner, 3), uint64(asNumberInt(t, row["satoshis"])))
			if err != nil {
				t.Fatal(err)
			}
			if claim.Alias != asString(t, row["alias"]) || claim.Domain != asString(t, row["domain"]) {
				t.Fatalf("decoded %q at %q", claim.Alias, claim.Domain)
			}
			if !bytes.Equal(claim.CertifierKey[:], fields[FieldCertifier]) {
				t.Fatal("certifier key changed during decoding")
			}
			if !bytes.Equal(claim.Signature, fields[FieldSignature]) {
				t.Fatal("signature changed during decoding")
			}
		})
	}
}

func TestDecodeStrictLockAfterShape(t *testing.T) {
	fields := decodeFirstPositiveFields(t)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	valid := decodeBuildLockAfter(t, fields, owner, 3)

	nonPushField := decodeFieldChunks(fields)
	nonPushField[FieldAlias] = &script.ScriptChunk{Op: script.OpDUP}
	nonPushField = append(nonPushField,
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: byte(len(owner)), Data: owner},
		&script.ScriptChunk{Op: script.OpCHECKSIG},
	)

	wrongMiddleDrop := decodeFieldChunks(fields)
	wrongMiddleDrop = append(wrongMiddleDrop,
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: script.OpDROP},
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: byte(len(owner)), Data: owner},
		&script.ScriptChunk{Op: script.OpCHECKSIG},
	)

	ownerBeforeDrops := decodeFieldChunks(fields)
	ownerBeforeDrops = append(ownerBeforeDrops,
		&script.ScriptChunk{Op: byte(len(owner)), Data: owner},
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: script.Op2DROP},
		&script.ScriptChunk{Op: script.OpCHECKSIG},
	)

	lockBefore := &script.Script{}
	decodeAppendPush(t, lockBefore, owner)
	decodeAppendOp(t, lockBefore, script.OpCHECKSIG)
	for _, field := range fields {
		decodeAppendPush(t, lockBefore, field)
	}
	decodeAppendOp(t, lockBefore, script.Op2DROP, script.Op2DROP, script.Op2DROP)

	nonMinimal := append(script.Script{script.OpPUSHDATA1, byte(len(fields[0]))}, (*valid)[1:]...)
	extraTrailing := append(script.Script(nil), (*valid)...)
	extraTrailing = append(extraTrailing, script.OpTRUE)
	wrongTerminal := append(script.Script(nil), (*valid)...)
	wrongTerminal[len(wrongTerminal)-1] = script.OpCHECKSIGVERIFY

	sevenFields := decodeCloneFields(fields)
	sevenFields = append(sevenFields, []byte("extra"))

	tests := []struct {
		name string
		scr  *script.Script
		want Code
	}{
		{name: "nil", scr: nil, want: CodeInvalidScript},
		{name: "empty", scr: &script.Script{}, want: CodeInvalidScript},
		{name: "truncated-pushdata", scr: &script.Script{script.OpPUSHDATA1}, want: CodeInvalidScript},
		{name: "five-fields", scr: decodeBuildLockAfter(t, fields[:5], owner, 3), want: CodeFieldCount},
		{name: "seven-fields", scr: decodeBuildLockAfter(t, sevenFields, owner, 3), want: CodeFieldCount},
		{name: "two-drops", scr: decodeBuildLockAfter(t, fields, owner, 2), want: CodeInvalidScript},
		{name: "four-drops", scr: decodeBuildLockAfter(t, fields, owner, 4), want: CodeInvalidScript},
		{name: "wrong-drop-opcode", scr: decodeScriptFromChunks(t, wrongMiddleDrop), want: CodeInvalidScript},
		{name: "non-push-field", scr: decodeScriptFromChunks(t, nonPushField), want: CodeInvalidScript},
		{name: "owner-before-drops", scr: decodeScriptFromChunks(t, ownerBeforeDrops), want: CodeInvalidScript},
		{name: "lock-before", scr: lockBefore, want: CodeInvalidScript},
		{name: "wrong-terminal-opcode", scr: &wrongTerminal, want: CodeInvalidScript},
		{name: "extra-after-checksig", scr: &extraTrailing, want: CodeInvalidScript},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim, err := Decode(tt.scr, 1)
			if claim != nil {
				t.Fatalf("unexpected claim: %+v", claim)
			}
			decodeAssertCode(t, err, tt.want)
		})
	}

	t.Run("non-minimal-push-is-still-an-operand", func(t *testing.T) {
		if _, err := Decode(&nonMinimal, 1); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDecodeRejectsTamperedSignedFields(t *testing.T) {
	fields := decodeFirstPositiveFields(t)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	otherCertifier := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey().Compressed()

	tests := []struct {
		name   string
		field  int
		value  []byte
		wanted Code
	}{
		{name: "field-order", field: FieldProtocol, value: []byte(ProtocolVersion), wanted: CodeInvalidProtocol},
		{name: "protocol", field: FieldProtocol, value: []byte("ecosystem_alias"), wanted: CodeInvalidProtocol},
		{name: "version", field: FieldVersion, value: []byte("2"), wanted: CodeInvalidVersion},
		{name: "alias", field: FieldAlias, value: []byte("other"), wanted: CodeInvalidSignature},
		{name: "domain", field: FieldDomain, value: []byte("other.example"), wanted: CodeInvalidSignature},
		{name: "certifier", field: FieldCertifier, value: otherCertifier, wanted: CodeInvalidSignature},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := decodeCloneFields(fields)
			mutated[tt.field] = append([]byte(nil), tt.value...)
			_, err := Decode(decodeBuildLockAfter(t, mutated, owner, 3), 1)
			decodeAssertCode(t, err, tt.wanted)
		})
	}
}

func TestDecodeRejectsBadSignaturesWithoutDoubleHashing(t *testing.T) {
	fields := decodeFirstPositiveFields(t)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	certifier := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000001")
	otherSigner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000003")

	var certifierKey [33]byte
	copy(certifierKey[:], fields[FieldCertifier])
	digest := Digest(string(fields[FieldAlias]), string(fields[FieldDomain]), certifierKey)
	doubleDigest := sha256.Sum256(digest[:])
	doubleHashedSignature, err := certifier.Sign(doubleDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	otherSignature, err := otherSigner.Sign(digest[:])
	if err != nil {
		t.Fatal(err)
	}

	flipped := append([]byte(nil), fields[FieldSignature]...)
	flipped[len(flipped)-1] ^= 0x01
	tests := []struct {
		name      string
		signature []byte
	}{
		{name: "empty", signature: nil},
		{name: "not-der", signature: []byte{0xde, 0xad, 0xbe, 0xef}},
		{name: "der-length-mismatch", signature: append(append([]byte(nil), fields[FieldSignature]...), 0x00)},
		{name: "zero-scalars", signature: []byte{0x30, 0x06, 0x02, 0x01, 0x00, 0x02, 0x01, 0x00}},
		{name: "tampered-der", signature: flipped},
		{name: "different-signer", signature: otherSignature.Serialize()},
		{name: "signature-of-double-sha256", signature: doubleHashedSignature.Serialize()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := decodeCloneFields(fields)
			mutated[FieldSignature] = append([]byte(nil), tt.signature...)
			_, err := Decode(decodeBuildLockAfter(t, mutated, owner, 3), 1)
			decodeAssertCode(t, err, CodeInvalidSignature)
		})
	}
}

func TestDecodeRejectsBadCertifierAndOwnerKeys(t *testing.T) {
	fields := decodeFirstPositiveFields(t)
	validOwner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	offCurve := append([]byte{0x02}, bytes.Repeat([]byte{0xff}, 32)...)

	certifierTests := []struct {
		name string
		key  []byte
	}{
		{name: "short", key: fields[FieldCertifier][:32]},
		{name: "uncompressed", key: validOwner.Uncompressed()},
		{name: "off-curve", key: offCurve},
	}
	for _, tt := range certifierTests {
		t.Run("certifier/"+tt.name, func(t *testing.T) {
			mutated := decodeCloneFields(fields)
			mutated[FieldCertifier] = append([]byte(nil), tt.key...)
			_, err := Decode(decodeBuildLockAfter(t, mutated, validOwner.Compressed(), 3), 1)
			decodeAssertCode(t, err, CodeInvalidCertifier)
		})
	}

	ownerTests := []struct {
		name string
		key  []byte
	}{
		{name: "short", key: validOwner.Compressed()[:32]},
		{name: "uncompressed", key: validOwner.Uncompressed()},
		{name: "off-curve", key: offCurve},
	}
	for _, tt := range ownerTests {
		t.Run("owner/"+tt.name, func(t *testing.T) {
			_, err := Decode(decodeBuildLockAfter(t, fields, tt.key, 3), 1)
			decodeAssertCode(t, err, CodeInvalidScript)
		})
	}
}

func TestDecodePreservesSignedNormalization(t *testing.T) {
	fields := decodeFirstPositiveFields(t)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()

	tests := []struct {
		name  string
		field int
		value string
		want  Code
	}{
		{name: "uppercase-alias", field: FieldAlias, value: "HandCash", want: CodeUnnormalizedToken},
		{name: "uppercase-domain", field: FieldDomain, value: "HandCash.IO", want: CodeUnnormalizedToken},
		{name: "leading-alias-space", field: FieldAlias, value: " handcash", want: CodeLeadingTrailingWhitespace},
		{name: "unicode-domain", field: FieldDomain, value: "bücher.example", want: CodeNonASCII},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := decodeCloneFields(fields)
			mutated[tt.field] = []byte(tt.value)
			_, err := Decode(decodeBuildLockAfter(t, mutated, owner, 3), 1)
			decodeAssertCode(t, err, tt.want)
		})
	}
}

func TestDecodeRequiresPositiveSatoshis(t *testing.T) {
	fields := decodeFirstPositiveFields(t)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	_, err := Decode(decodeBuildLockAfter(t, fields, owner, 3), 0)
	decodeAssertCode(t, err, CodeNonPositiveValue)
}

func TestDecodeCopiesSignature(t *testing.T) {
	fields := decodeFirstPositiveFields(t)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	lockingScript := decodeBuildLockAfter(t, fields, owner, 3)
	claim, err := Decode(lockingScript, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), claim.Signature...)

	chunks, err := lockingScript.Chunks()
	if err != nil {
		t.Fatal(err)
	}
	chunks[FieldSignature].Data[len(chunks[FieldSignature].Data)-1] ^= 0x01
	if !bytes.Equal(claim.Signature, want) {
		t.Fatal("returned signature aliases locking-script storage")
	}
}

func decodeFirstPositiveFields(t *testing.T) [][]byte {
	t.Helper()
	doc, _ := loadFixture(t)
	row := fixtureSlice(t, doc, "tokens", "positive")[0].(map[string]any)
	return decodeFixtureFields(t, row)
}

func decodeFixtureFields(t *testing.T, row map[string]any) [][]byte {
	t.Helper()
	key, err := hex.DecodeString(asString(t, row["certifierKeyHex"]))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := hex.DecodeString(asString(t, row["signatureDerHex"]))
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{
		[]byte(asString(t, row["protocol"])),
		[]byte(asString(t, row["version"])),
		[]byte(asString(t, row["alias"])),
		[]byte(asString(t, row["domain"])),
		key,
		sig,
	}
}

func decodeBuildLockAfter(t *testing.T, fields [][]byte, owner []byte, drops int) *script.Script {
	t.Helper()
	s := &script.Script{}
	for _, field := range fields {
		decodeAppendPush(t, s, field)
	}
	for range drops {
		decodeAppendOp(t, s, script.Op2DROP)
	}
	decodeAppendPush(t, s, owner)
	decodeAppendOp(t, s, script.OpCHECKSIG)
	return s
}

func decodeFieldChunks(fields [][]byte) []*script.ScriptChunk {
	chunks := make([]*script.ScriptChunk, len(fields))
	for i, field := range fields {
		chunks[i] = &script.ScriptChunk{Op: byte(len(field)), Data: append([]byte(nil), field...)}
	}
	return chunks
}

func decodeScriptFromChunks(t *testing.T, chunks []*script.ScriptChunk) *script.Script {
	t.Helper()
	s, err := script.NewScriptFromScriptOps(chunks)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func decodeAppendPush(t *testing.T, s *script.Script, data []byte) {
	t.Helper()
	if err := s.AppendPushData(data); err != nil {
		t.Fatal(err)
	}
}

func decodeAppendOp(t *testing.T, s *script.Script, ops ...uint8) {
	t.Helper()
	if err := s.AppendOpcodes(ops...); err != nil {
		t.Fatal(err)
	}
}

func decodePrivateKey(t *testing.T, keyHex string) *ec.PrivateKey {
	t.Helper()
	key, err := ec.PrivateKeyFromHex(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func decodeCloneFields(fields [][]byte) [][]byte {
	out := make([][]byte, len(fields))
	for i, field := range fields {
		out[i] = append([]byte(nil), field...)
	}
	return out
}

func decodeAssertCode(t *testing.T, err error, want Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", want)
	}
	got, ok := CodeOf(err)
	if !ok || got != want {
		t.Fatalf("got %v (%s) want %s", err, got, want)
	}
}
