package gib

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	testOrigin = "c657be5a7dacd7bb7343d92b7195d1366dbecd3ec31874576189efd28eee007c_0"
	testRoot   = "e6f27ce723b2923e93227ecef64b6cecf9464bd0c6ba71a66502aa20c5de82a1_3"
	testFork   = "83c55ad839d8bca1042909663b6a1d7468e7dfd71c67cd43d52363a254359138_1"

	// Verified with `git hash-object -t commit`.
	testCommit    = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nparent 1111111111111111111111111111111111111111\nauthor Ada Lovelace <ada@example.com> 1700000000 +0100\ncommitter Bob <bob@example.com> 1700000600 -0500\n\nInitial commit\n\nbody line\n"
	testCommitSHA = "2c5492c77177c511c4dc3585721d4ec9c40320bd"
)

func testKey(t *testing.T, seed string) *ec.PrivateKey {
	t.Helper()
	key, err := ec.PrivateKeyFromHex(seed)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testFields(t *testing.T, identity *ec.PublicKey, branchedFrom string) [][]byte {
	t.Helper()
	fields, err := Fields(testOrigin, "main", testRoot, identity, branchedFrom)
	if err != nil {
		t.Fatal(err)
	}
	return fields
}

func appendPush(t *testing.T, s *script.Script, data []byte) {
	t.Helper()
	if err := s.AppendPushData(data); err != nil {
		t.Fatal(err)
	}
}

func appendOps(t *testing.T, s *script.Script, ops ...uint8) {
	t.Helper()
	if err := s.AppendOpcodes(ops...); err != nil {
		t.Fatal(err)
	}
}

func lockAfter(t *testing.T, fields [][]byte, lockKey *ec.PublicKey) *script.Script {
	t.Helper()
	s := &script.Script{}
	for _, f := range fields {
		appendPush(t, s, f)
	}
	for i := 0; i < len(fields)/2; i++ {
		appendOps(t, s, script.Op2DROP)
	}
	if len(fields)%2 == 1 {
		appendOps(t, s, script.OpDROP)
	}
	appendPush(t, s, lockKey.Compressed())
	appendOps(t, s, script.OpCHECKSIG)
	return s
}

func TestDecodeReferenceShape(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	lockKey := testKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey()

	s, err := LockingScript(lockKey, testFields(t, identity, ""))
	if err != nil {
		t.Fatal(err)
	}
	head, err := Decode(s, 1)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if head.Origin != testOrigin || head.Branch != "main" || head.Root != testRoot {
		t.Fatalf("fields = %+v", head)
	}
	if head.Identity != hex.EncodeToString(identity.Compressed()) {
		t.Fatalf("identity = %s", head.Identity)
	}
	if head.LockingKey != hex.EncodeToString(lockKey.Compressed()) {
		t.Fatalf("locking key = %s", head.LockingKey)
	}
	// An ordinary push names no second parent, and the absent field is an
	// empty push: PushDrop writes it as OP_FALSE, one byte.
	if head.BranchedFrom != "" {
		t.Fatalf("branched from = %q, want empty", head.BranchedFrom)
	}
	chunks, err := s.Chunks()
	if err != nil {
		t.Fatal(err)
	}
	if got := chunks[len(chunks)-4]; got.Op != script.Op0 || len(got.Data) != 0 {
		t.Fatalf("absent branched-from encoded as op %d (%x)", got.Op, got.Data)
	}
}

func TestDecodeBranchedFrom(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	lockKey := testKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey()

	s, err := LockingScript(lockKey, testFields(t, identity, testFork))
	if err != nil {
		t.Fatal(err)
	}
	head, err := Decode(s, 1)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if head.BranchedFrom != testFork {
		t.Fatalf("branched from = %q, want %s", head.BranchedFrom, testFork)
	}

	// A single zero byte is the same opcode as an empty push once on chain,
	// so it reads back as absent rather than as a bogus outpoint.
	zero := testFields(t, identity, "")
	zero[FieldBranchedFrom] = []byte{0x00}
	zeroScript, err := LockingScript(lockKey, zero)
	if err != nil {
		t.Fatal(err)
	}
	if head, err := Decode(zeroScript, 1); err != nil || head.BranchedFrom != "" {
		t.Fatalf("zero byte branched-from = %+v, %v", head, err)
	}
}

func TestDecodeVariants(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	lockKey := testKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey()
	origin, _ := transaction.OutpointFromString(testOrigin)
	root, _ := transaction.OutpointFromString(testRoot)
	fork, _ := transaction.OutpointFromString(testFork)

	binaryFields := [][]byte{
		[]byte("gib"), origin.Bytes(), []byte("feature/x"), root.Bytes(),
		[]byte(hex.EncodeToString(identity.Compressed())), fork.Bytes(),
	}
	sealed := append(testFields(t, identity, ""), []byte("signature-bytes"))

	plain, _ := LockingScript(lockKey, testFields(t, identity, ""))
	binary, _ := LockingScript(lockKey, binaryFields)
	sealedScript, _ := LockingScript(lockKey, sealed)

	tests := []struct {
		name   string
		script *script.Script
		branch string
		from   string
	}{
		{"lock-before", plain, "main", ""},
		{"binary outpoints and hex identity", binary, "feature/x", testFork},
		{"trailing signature field ignored", sealedScript, "main", ""},
		{"lock-after", lockAfter(t, testFields(t, identity, ""), lockKey), "main", ""},
		{"lock-after with a fork", lockAfter(t, testFields(t, identity, testFork), lockKey), "main", testFork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head, err := Decode(tt.script, 1)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if head.Branch != tt.branch || head.Origin != testOrigin || head.Root != testRoot {
				t.Fatalf("head = %+v", head)
			}
			if head.BranchedFrom != tt.from {
				t.Fatalf("branched from = %q, want %q", head.BranchedFrom, tt.from)
			}
		})
	}
}

func mustLock(t *testing.T, key *ec.PublicKey, fields [][]byte) *script.Script {
	t.Helper()
	s, err := LockingScript(key, fields)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The five-field heads with the commit inscribed on the same output are a
// different format. They must stop decoding: that is the break, and there
// is no compatibility path.
func TestDecodeRejectsInscribedFiveFieldHeads(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	lockKey := testKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey()
	old := [][]byte{
		[]byte("gib"), []byte(testOrigin), []byte("main"), []byte(testRoot),
		identity.Compressed(),
	}
	if _, err := Decode(mustLock(t, lockKey, old), 1); !errors.Is(err, ErrFieldCount) {
		t.Fatalf("bare five-field head: err = %v, want ErrFieldCount", err)
	}

	// The same five fields behind an ordinal inscription envelope: the
	// shape gib published until now.
	inscribed := &script.Script{}
	appendOps(t, inscribed, script.OpFALSE, script.OpIF)
	appendPush(t, inscribed, []byte("ord"))
	appendOps(t, inscribed, script.Op1)
	appendPush(t, inscribed, []byte("application/x-git-commit"))
	appendOps(t, inscribed, script.Op0)
	appendPush(t, inscribed, []byte(testCommit))
	appendOps(t, inscribed, script.OpENDIF)
	inscribed = script.NewFromBytes(append(*inscribed, *mustLock(t, lockKey, old)...))
	if _, err := Decode(inscribed, 1); err == nil {
		t.Fatal("inscribed five-field head decoded; want a refusal")
	}
}

func TestDecodeRejects(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	lockKey := testKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey()
	good := testFields(t, identity, "")

	clone := func(mod func(f [][]byte)) *script.Script {
		f := make([][]byte, len(good))
		copy(f, good)
		mod(f)
		return mustLock(t, lockKey, f)
	}
	p2pkh, _ := script.NewFromHex("76a914000000000000000000000000000000000000000088ac")

	tests := []struct {
		name   string
		script *script.Script
		sats   uint64
		want   error
	}{
		{"zero sats", clone(func([][]byte) {}), 0, ErrNotOneSat},
		{"many sats", clone(func([][]byte) {}), 100, ErrNotOneSat},
		{"p2pkh", p2pkh, 1, ErrNotPushDrop},
		{"wrong protocol", clone(func(f [][]byte) { f[FieldProtocol] = []byte("gab") }), 1, ErrProtocol},
		{"too few fields", mustLock(t, lockKey, good[:5]), 1, ErrFieldCount},
		{"too many fields", mustLock(t, lockKey, append(append([][]byte{}, good...), []byte("sig"), []byte("extra"))), 1, ErrFieldCount},
		{"bad origin", clone(func(f [][]byte) { f[FieldOrigin] = []byte("nope") }), 1, ErrOrigin},
		{"bad root", clone(func(f [][]byte) { f[FieldRoot] = []byte("nope") }), 1, ErrRoot},
		{"bad branched from", clone(func(f [][]byte) { f[FieldBranchedFrom] = []byte("nope") }), 1, ErrBranchedFrom},
		{"empty branch", clone(func(f [][]byte) { f[FieldBranch] = []byte{} }), 1, ErrBranch},
		{"control char branch", clone(func(f [][]byte) { f[FieldBranch] = []byte("ma\x00in") }), 1, ErrBranch},
		{"long branch", clone(func(f [][]byte) { f[FieldBranch] = []byte(strings.Repeat("a", 256)) }), 1, ErrBranch},
		{"bad identity", clone(func(f [][]byte) { f[FieldIdentity] = []byte("short") }), 1, ErrIdentity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.script, tt.sats)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFieldsRejects(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	if _, err := Fields(testOrigin, "main", testRoot, identity, "not-an-outpoint"); !errors.Is(err, ErrBranchedFrom) {
		t.Fatalf("err = %v, want ErrBranchedFrom", err)
	}
	if _, err := Fields(testOrigin, "main", testRoot, nil, ""); !errors.Is(err, ErrIdentity) {
		t.Fatalf("err = %v, want ErrIdentity", err)
	}
}

func TestParseCommit(t *testing.T) {
	c, err := ParseCommit([]byte(testCommit))
	if err != nil {
		t.Fatal(err)
	}
	if c.SHA != testCommitSHA {
		t.Fatalf("sha = %s, want %s", c.SHA, testCommitSHA)
	}
	if c.Tree != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" || len(c.Parents) != 1 || c.Parents[0] != strings.Repeat("1", 40) {
		t.Fatalf("tree/parents = %s %v", c.Tree, c.Parents)
	}
	if c.Author == nil || c.Author.Name != "Ada Lovelace" || c.Author.Email != "ada@example.com" || c.Author.Time != 1700000000 || c.Author.TZ != "+0100" {
		t.Fatalf("author = %+v", c.Author)
	}
	if c.Committer == nil || c.Committer.Name != "Bob" || c.Committer.Time != 1700000600 || c.Committer.TZ != "-0500" {
		t.Fatalf("committer = %+v", c.Committer)
	}
	if c.Message != "Initial commit\n\nbody line\n" {
		t.Fatalf("message = %q", c.Message)
	}
	if !IsObjectID(c.SHA) || IsObjectID("nope") {
		t.Fatal("IsObjectID")
	}
}

func TestParseCommitGpgsigAndRoot(t *testing.T) {
	raw := "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nauthor A <a@x> 1 +0000\ncommitter A <a@x> 1 +0000\ngpgsig -----BEGIN PGP SIGNATURE-----\n abc\n -----END PGP SIGNATURE-----\n\nmsg\n"
	c, err := ParseCommit([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Parents) != 0 || c.Message != "msg\n" {
		t.Fatalf("commit = %+v", c)
	}
	if c.SHA != ObjectID("commit", []byte(raw)) {
		t.Fatal("sha mismatch")
	}
}

func TestParseCommitRejects(t *testing.T) {
	for _, raw := range []string{"", "hello", "tree short\n\nmsg", "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nparent zz\n\nx"} {
		if _, err := ParseCommit([]byte(raw)); !errors.Is(err, ErrNotCommit) {
			t.Fatalf("%q: err = %v, want ErrNotCommit", raw, err)
		}
	}
}
