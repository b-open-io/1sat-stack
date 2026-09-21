package gib

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Building the content a push publishes, the way gib does: 0-sat B data
// outputs holding the commit objects, the `.git` object store naming them,
// and the root directory that names `.git` beside git's own tree. These
// helpers are what lets the tests submit a head with its push instead of on
// its own, which is what admission now requires.

// dirEnt is one entry of a directory under construction. Exactly one of
// vout (a sibling output of the same transaction) and op (an outpoint
// elsewhere) is set.
type dirEnt struct {
	name  string
	isDir bool
	vout  int
	op    string
}

func sameTx(name string, vout int, isDir bool) dirEnt {
	return dirEnt{name: name, vout: vout, isDir: isDir, op: ""}
}

func elsewhere(name, outpoint string, isDir bool) dirEnt {
	return dirEnt{name: name, vout: -1, op: outpoint, isDir: isDir}
}

// encodeDir writes a canonical `ordfs/dir` manifest: version, entry count,
// then entries sorted by raw name bytes.
func encodeDir(t *testing.T, entries []dirEnt) []byte {
	t.Helper()
	sorted := append([]dirEnt(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	out := []byte{0x01, 0x00, 0x00}
	binary.BigEndian.PutUint16(out[1:3], uint16(len(sorted)))
	for _, e := range sorted {
		var flags byte
		if e.isDir {
			flags |= 0x01
		}
		if e.op != "" {
			flags |= 0x08
		}
		out = append(out, flags, byte(len(e.name)))
		out = append(out, e.name...)
		if e.op != "" {
			op, err := transaction.OutpointFromString(e.op)
			if err != nil {
				t.Fatalf("encodeDir: %v", err)
			}
			out = append(out, op.Bytes()...)
			continue
		}
		out = append(out, byte(e.vout))
	}
	return out
}

// dataScript is a standalone 0-sat B output: OP_FALSE OP_RETURN | B | data
// | media type | encoding, the shape gib publishes content in.
func dataScript(t *testing.T, content []byte, contentType string) *script.Script {
	t.Helper()
	s := &script.Script{}
	if err := s.AppendOpcodes(script.OpFALSE, script.OpRETURN); err != nil {
		t.Fatal(err)
	}
	for _, push := range [][]byte{[]byte(bitcom.BPrefix), content, []byte(contentType), []byte("binary")} {
		if err := s.AppendPushData(push); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func dataOutput(t *testing.T, content []byte, contentType string) *transaction.TransactionOutput {
	t.Helper()
	return &transaction.TransactionOutput{Satoshis: 0, LockingScript: dataScript(t, content, contentType)}
}

// newContentTx starts a content transaction. The nonce keeps otherwise
// identical pushes from colliding on a txid.
func newContentTx(t *testing.T, nonce byte) *transaction.Transaction {
	t.Helper()
	tx := transaction.NewTransaction()
	source := hex.EncodeToString(bytes.Repeat([]byte{nonce}, 32))
	if err := tx.AddInputFrom(source, 0, "76a914000000000000000000000000000000000000000088ac", 1, nil); err != nil {
		t.Fatal(err)
	}
	tx.Inputs[0].UnlockingScript = &script.Script{}
	return tx
}

// push is a published root: the content transaction and the outpoints a
// head (and the next push) needs to name it.
type push struct {
	tx   *transaction.Transaction
	root string
	// objects maps each commit sha to the outpoint holding its object, so
	// a later push can cite what this one published instead of repeating it.
	objects map[string]string
	tip     string
}

// publish writes one push's content: each commit object in commits as its
// own output, a `.git` store naming those plus everything in cited, and the
// root directory. tip names the commit the `.` entry points at, given
// either as the raw commit object or as its sha; it must be one of commits
// or one of cited.
func publish(t *testing.T, nonce byte, commits []string, cited map[string]string, tip string) *push {
	t.Helper()
	tx := newContentTx(t, nonce)
	objects := map[string]string{}
	store := make([]dirEnt, 0, len(commits)+len(cited)+1)
	tipSha := tip
	if !gibtpl.IsObjectID(tipSha) {
		tipSha = gibtpl.ObjectID("commit", []byte(tip))
	}
	var tipEnt *dirEnt

	for i, raw := range commits {
		sha := gibtpl.ObjectID("commit", []byte(raw))
		tx.AddOutput(dataOutput(t, []byte(raw), GitCommitContentType))
		e := sameTx(sha, i, false)
		store = append(store, e)
		if sha == tipSha {
			ent := e
			tipEnt = &ent
		}
	}
	for sha, op := range cited {
		e := elsewhere(sha, op, false)
		store = append(store, e)
		if sha == tipSha {
			ent := e
			tipEnt = &ent
		}
	}
	if tipEnt == nil {
		t.Fatalf("publish: tip %s is neither published nor cited", tipSha)
	}
	dot := *tipEnt
	dot.name = GitDirTipEntry
	store = append(store, dot)

	fileVout := len(commits)
	tx.AddOutput(dataOutput(t, []byte{'r', 'e', 'a', 'd', 'm', 'e', nonce}, "text/plain"))
	storeVout := fileVout + 1
	tx.AddOutput(dataOutput(t, encodeDir(t, store), ordfs.DirContentType))
	rootVout := storeVout + 1
	tx.AddOutput(dataOutput(t, encodeDir(t, []dirEnt{
		sameTx("README.md", fileVout, false),
		sameTx(GitDir, storeVout, true),
	}), ordfs.DirContentType))
	prove(tx, 900000)

	p := &push{tx: tx, objects: objects, tip: tipSha}
	for i, raw := range commits {
		p.objects[gibtpl.ObjectID("commit", []byte(raw))] = op(tx, uint32(i))
	}
	for sha, ref := range cited {
		p.objects[sha] = ref
	}
	p.root = op(tx, uint32(rootVout))
	return p
}

// publishRootOnly writes a root directory with no `.git` store: a tree, but
// not a gib push.
func publishRootOnly(t *testing.T, nonce byte) *push {
	t.Helper()
	tx := newContentTx(t, nonce)
	tx.AddOutput(dataOutput(t, []byte{'r', 'e', 'a', 'd', 'm', 'e', nonce}, "text/plain"))
	tx.AddOutput(dataOutput(t, encodeDir(t, []dirEnt{sameTx("README.md", 0, false)}), ordfs.DirContentType))
	prove(tx, 900000)
	return &push{tx: tx, root: op(tx, 1), objects: map[string]string{}}
}

// submissionBeef serializes the transactions as one BEEF V2 whose subject
// is the last: a push submits its content transactions alongside the head,
// and an atomic BEEF would prune everything the head does not spend.
func submissionBeef(t *testing.T, txs ...*transaction.Transaction) []byte {
	t.Helper()
	beef := transaction.NewBeef()
	for _, tx := range txs {
		if _, err := beef.MergeTransaction(tx); err != nil {
			t.Fatal(err)
		}
	}
	beef.NewestTxID = txs[len(txs)-1].TxID()
	b, err := beef.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
