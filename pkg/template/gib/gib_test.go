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

func testFields(t *testing.T, identity *ec.PublicKey) [][]byte {
	t.Helper()
	fields, err := Fields(testOrigin, "main", testRoot, identity)
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
	fields := testFields(t, identity)

	s, err := LockingScript(lockKey, fields, []byte(testCommit), "application/x-git-commit")
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
	if head.ContentType != "application/x-git-commit" {
		t.Fatalf("content type = %q", head.ContentType)
	}
	if head.Commit == nil || head.Commit.SHA != testCommitSHA {
		t.Fatalf("commit = %+v", head.Commit)
	}
}

func TestDecodeVariants(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	lockKey := testKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey()
	origin, _ := transaction.OutpointFromString(testOrigin)
	root, _ := transaction.OutpointFromString(testRoot)

	binaryFields := [][]byte{[]byte("gib"), origin.Bytes(), []byte("feature/x"), root.Bytes(), []byte(hex.EncodeToString(identity.Compressed()))}
	sealed := append(testFields(t, identity), []byte("signature-bytes"))

	noCommit, _ := LockingScript(lockKey, testFields(t, identity), nil, "")
	binary, _ := LockingScript(lockKey, binaryFields, nil, "")
	sealedScript, _ := LockingScript(lockKey, sealed, nil, "")

	// Inscription after the lock (suffix form).
	envelope, _ := LockingScript(lockKey, [][]byte{[]byte("x")}, []byte(testCommit), "text/plain")
	envelopeOnly := script.NewFromBytes((*envelope)[:len(*envelope)-len(*mustLock(t, lockKey, [][]byte{[]byte("x")}))])
	suffixForm := script.NewFromBytes(append(append([]byte{}, *noCommit...), *envelopeOnly...))

	tests := []struct {
		name   string
		script *script.Script
		branch string
		commit bool
	}{
		{"lock-before without commit", noCommit, "main", false},
		{"binary outpoints and hex identity", binary, "feature/x", false},
		{"trailing signature field ignored", sealedScript, "main", false},
		{"lock-after", lockAfter(t, testFields(t, identity), lockKey), "main", false},
		{"inscription suffix", suffixForm, "main", true},
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
			if (head.Commit != nil) != tt.commit {
				t.Fatalf("commit present = %v, want %v", head.Commit != nil, tt.commit)
			}
		})
	}
}

func mustLock(t *testing.T, key *ec.PublicKey, fields [][]byte) *script.Script {
	t.Helper()
	s, err := LockingScript(key, fields, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDecodeRejects(t *testing.T) {
	identity := testKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey()
	lockKey := testKey(t, "0000000000000000000000000000000000000000000000000000000000000003").PubKey()
	good := testFields(t, identity)

	clone := func(mod func(f [][]byte)) *script.Script {
		f := make([][]byte, len(good))
		copy(f, good)
		mod(f)
		s, err := LockingScript(lockKey, f, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return s
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
		{"too few fields", mustLock(t, lockKey, good[:4]), 1, ErrFieldCount},
		{"bad origin", clone(func(f [][]byte) { f[FieldOrigin] = []byte("nope") }), 1, ErrOrigin},
		{"bad root", clone(func(f [][]byte) { f[FieldRoot] = []byte("nope") }), 1, ErrRoot},
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
