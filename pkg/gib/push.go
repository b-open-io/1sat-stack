package gib

import (
	"errors"
	"fmt"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Reading the push a head publishes, out of the submission and nothing else.
//
// A head names a root: an `ordfs/dir` holding git's tree for the tip commit
// plus one extra entry, `.git`, the repository's object store. Every commit
// object reachable from the tip is in that store, named by its sha, and its
// `.` entry points at the tip commit. gib strips `.git` before hashing, so
// the tree still verifies against what git computed.
//
// Because the store is keyed by sha, a name is proof of content: an object
// already on chain is cited at the outpoint that holds it rather than
// republished, which is what lets a branch fork from another publisher's
// head without copying anything. So a `.git` entry is either an output of
// the transaction this submission carries — which the overlay reads and
// verifies here — or a citation of an earlier one, which it records and may
// not hold. One hop verified, every hop named: the overlay is not required
// to hold the whole history, and admission never asks it to.

const (
	// GitDir is the object store entry gib adds to a published root. git
	// itself refuses to put a `.git` entry in a tree, so the name can never
	// collide with a real file.
	GitDir = ".git"
	// GitDirTipEntry is the default entry of the object store: it points at
	// the tip commit object, so a reader finds which commit a head
	// publishes without reading every object in the store.
	GitDirTipEntry = "."
	// GitCommitContentType is what gib writes on a published commit object.
	// It is not required: a `.git` entry is proved by its sha, not by the
	// content type the writer chose.
	GitCommitContentType = "application/x-git-commit"
)

var (
	// ErrRootMissing is returned when the submission does not carry the
	// transaction holding the head's root.
	ErrRootMissing = errors.New("gib: the transaction holding the head's root is not in the submission")
	// ErrRootNotDir is returned when the head's root is not an `ordfs/dir`.
	ErrRootNotDir = errors.New("gib: the head's root does not decode as an ordfs/dir")
	// ErrNoGitDir is returned when the published root has no `.git` entry.
	ErrNoGitDir = errors.New("gib: the head's root has no .git object store")
	// ErrGitDirUnreadable is returned when the `.git` entry does not resolve
	// to a directory the submission carries.
	ErrGitDirUnreadable = errors.New("gib: the .git object store is not readable from the submission")
	// ErrCommitObject is returned when a `.git` entry the submission carries
	// is not the git commit object its name claims.
	ErrCommitObject = errors.New("gib: a .git entry is not the commit object its name claims")
	// ErrBranchedFromMissing is returned when the branched-from field names
	// a head the submission does not carry.
	ErrBranchedFromMissing = errors.New("gib: the transaction holding the branched-from head is not in the submission")
)

// CommitObject is one commit named by a published root's `.git` store. Ref
// is the output holding the object. Commit is nil when that output is in a
// transaction the submission does not carry: the hop is named, not held.
type CommitObject struct {
	Sha    string
	Ref    *transaction.Outpoint
	Commit *gibtpl.Commit
	Tip    bool
}

// Held reports whether the object's bytes were read out of the submission.
func (c CommitObject) Held() bool { return c.Commit != nil }

// Push is what a submission says about the push a head publishes: the root
// it names, the object store in it, and the commits that store names.
type Push struct {
	// Root is the published root directory the head points at.
	Root *transaction.Outpoint
	// GitDir is the `.git` object store inside that root.
	GitDir *transaction.Outpoint
	// TipSha is the commit the head publishes, found by matching the store's
	// `.` entry against the entry named by a sha. Empty when the store has
	// no `.` entry.
	TipSha string
	// Tip is the tip commit object, when the submission carried it.
	Tip *gibtpl.Commit
	// Objects is every commit the store names, held or only cited.
	Objects []CommitObject
	// Txids is every transaction this reading relied on, excluding the
	// head's own. They are returned to the engine as AncillaryTxids so they
	// are retained with the head instead of discarded after the submission.
	Txids []*chainhash.Hash
}

// ReadPush reads and checks a head's push from the submitted BEEF alone. No
// network fetch: everything it needs is either in the submission or the head
// is not admissible. headTxid may be nil, in which case the head's own
// transaction is not excluded from Txids.
func ReadPush(beef *transaction.Beef, head *gibtpl.Head, headTxid *chainhash.Hash) (*Push, error) {
	if beef == nil || head == nil {
		return nil, ErrRootMissing
	}
	root, err := transaction.OutpointFromString(head.Root)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRootNotDir, err)
	}
	p := &Push{Root: root}
	seen := map[chainhash.Hash]bool{}
	if headTxid != nil {
		seen[*headTxid] = true
	}
	relied := func(txid chainhash.Hash) {
		if seen[txid] {
			return
		}
		seen[txid] = true
		hash := txid
		p.Txids = append(p.Txids, &hash)
	}

	// The root must be in the submission and must be a directory.
	rootType, rootBytes, ok := outputContent(beef, root)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRootMissing, root.OrdinalString())
	}
	if rootType != ordfs.DirContentType {
		return nil, fmt.Errorf("%w: %s is %q", ErrRootNotDir, root.OrdinalString(), rootType)
	}
	rootEntries, err := ordfs.DecodeDir(rootBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrRootNotDir, root.OrdinalString(), err)
	}
	relied(root.Txid)

	// It must carry the object store, and the store must be readable here:
	// without it the overlay knows nothing about the commits this push
	// publishes.
	var store *transaction.Outpoint
	for _, e := range rootEntries {
		if e.Name != GitDir {
			continue
		}
		if !e.IsDir {
			return nil, fmt.Errorf("%w: %s is not a directory", ErrNoGitDir, GitDir)
		}
		store = e.Target(root)
		break
	}
	if store == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoGitDir, root.OrdinalString())
	}
	p.GitDir = store

	storeType, storeBytes, ok := outputContent(beef, store)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrGitDirUnreadable, store.OrdinalString())
	}
	if storeType != ordfs.DirContentType {
		return nil, fmt.Errorf("%w: %s is %q", ErrGitDirUnreadable, store.OrdinalString(), storeType)
	}
	storeEntries, err := ordfs.DecodeDir(storeBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrGitDirUnreadable, store.OrdinalString(), err)
	}
	relied(store.Txid)

	var tipRef *transaction.Outpoint
	for _, e := range storeEntries {
		if e.Name == GitDirTipEntry {
			tipRef = e.Target(store)
			continue
		}
		// Names that are not object ids belong to whatever the store grows
		// next; a reader that does not know them leaves them alone.
		if e.IsDir || !gibtpl.IsObjectID(e.Name) {
			continue
		}
		ref := e.Target(store)
		if ref == nil {
			return nil, fmt.Errorf("%w: %s has no target", ErrCommitObject, e.Name)
		}
		obj := CommitObject{Sha: e.Name, Ref: ref}
		// A name is proof of content, so an object the submission carries is
		// checked against its name. One it does not carry is a citation of
		// an earlier transaction: recorded, not fetched, not required.
		if _, content, ok := outputContent(beef, ref); ok {
			commit, err := gibtpl.ParseCommit(content)
			if err != nil {
				return nil, fmt.Errorf("%w: %s at %s: %v", ErrCommitObject, e.Name, ref.OrdinalString(), err)
			}
			if commit.SHA != e.Name {
				return nil, fmt.Errorf("%w: %s at %s hashes to %s", ErrCommitObject, e.Name, ref.OrdinalString(), commit.SHA)
			}
			obj.Commit = commit
			relied(ref.Txid)
		}
		p.Objects = append(p.Objects, obj)
	}

	if tipRef != nil {
		for i, obj := range p.Objects {
			if *obj.Ref != *tipRef {
				continue
			}
			p.Objects[i].Tip = true
			p.TipSha = obj.Sha
			p.Tip = obj.Commit
			break
		}
	}

	// A branch's first head, and a merge, name a second parent. The head it
	// names must be in the submission too: a parent nothing carries is a
	// claim, not a link.
	if head.BranchedFrom != "" {
		from, err := transaction.OutpointFromString(head.BranchedFrom)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBranchedFromMissing, err)
		}
		tx := beef.FindTransactionByHash(&from.Txid)
		if tx == nil || int(from.Index) >= len(tx.Outputs) || tx.Outputs[from.Index] == nil {
			return nil, fmt.Errorf("%w: %s", ErrBranchedFromMissing, from.OrdinalString())
		}
		out := tx.Outputs[from.Index]
		if _, err := gibtpl.Decode(out.LockingScript, out.Satoshis); err != nil {
			return nil, fmt.Errorf("%w: %s is not a head: %v", ErrBranchedFromMissing, from.OrdinalString(), err)
		}
		relied(from.Txid)
	}

	return p, nil
}

// outputContent reads an output's published content out of the BEEF. ok is
// false when the submission does not carry the transaction, the output does
// not exist, or the output publishes no content.
func outputContent(beef *transaction.Beef, op *transaction.Outpoint) (string, []byte, bool) {
	if op == nil {
		return "", nil, false
	}
	tx := beef.FindTransactionByHash(&op.Txid)
	if tx == nil || int(op.Index) >= len(tx.Outputs) {
		return "", nil, false
	}
	out := tx.Outputs[op.Index]
	if out == nil || out.LockingScript == nil {
		return "", nil, false
	}
	contentType, content, _, _ := ordfs.ParseOutputForContent(out)
	if content == nil {
		return "", nil, false
	}
	return contentType, content, true
}
