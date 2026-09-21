package gib

import (
	"context"
	"log/slog"

	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicManager admits gib commit heads into tm_gib.
type TopicManager struct {
	Logger *slog.Logger
}

var _ engine.TopicManager = (*TopicManager)(nil)

func (tm *TopicManager) logger() *slog.Logger {
	if tm.Logger != nil {
		return tm.Logger
	}
	return slog.Default()
}

// IdentifyAdmissibleOutputs admits a commit head only when the submission
// carries the push it claims to publish, judged from the BEEF alone with no
// network fetch:
//
//   - the transaction holding the head's root is present, and its output at
//     that index decodes as an `ordfs/dir`;
//   - that directory has a `.git` object store, readable here, and every
//     commit object in it that the submission carries hashes to the sha it
//     is filed under;
//   - when the branched-from field is set, the transaction holding that
//     head is present too.
//
// What it deliberately does NOT require is that the overlay hold the whole
// history. A `.git` entry citing a commit object in an earlier transaction
// is a reference the overlay records and may not hold: one hop verified,
// every hop named.
//
// Every transaction the reading relied on is returned as an ancillary txid,
// so it is retained with the head instead of discarded once the submission
// is over.
//
// Spent heads are not retained in the engine: the lookup service keeps the
// full push history in its own table.
func (tm *TopicManager) IdentifyAdmissibleOutputs(_ context.Context, beef *transaction.Beef, txid *chainhash.Hash, _ []uint32) (admit overlay.AdmittanceInstructions, err error) {
	if beef == nil || txid == nil {
		return admit, engine.ErrInvalidBeef
	}
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}
	seen := map[chainhash.Hash]bool{}
	for vout, output := range tx.Outputs {
		if output == nil {
			continue
		}
		head, err := gibtpl.Decode(output.LockingScript, output.Satoshis)
		if err != nil {
			continue
		}
		push, err := ReadPush(beef, head, txid)
		if err != nil {
			// Not an error for the submission: one output of it is not an
			// admissible head. Others in the same transaction still are.
			tm.logger().Debug("gib: head refused",
				"txid", txid.String(), "vout", vout,
				"origin", head.Origin, "branch", head.Branch, "root", head.Root,
				"error", err)
			continue
		}
		admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		for _, hash := range push.Txids {
			if hash == nil || seen[*hash] {
				continue
			}
			seen[*hash] = true
			admit.AncillaryTxids = append(admit.AncillaryTxids, hash)
		}
	}
	return admit, nil
}

// IdentifyNeededInputs returns nothing: a head is judged from its own
// locking script and the content the submission carries, so no GASP
// dependency resolution is required.
func (tm *TopicManager) IdentifyNeededInputs(_ context.Context, _ *transaction.Beef, _ *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *TopicManager) GetDocumentation() string {
	return "gib commit heads: bare 1-sat PushDrop coins with fields " +
		"[\"gib\", repository origin, branch, root, identity, branched-from]. " +
		"A head is admitted only when the submission carries the transaction " +
		"holding its root, that root is an ordfs/dir with a .git object store, " +
		"and any branched-from head is present too."
}

// GetMetaData returns metadata for the topic.
func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name:        TopicName,
		Description: "gib on-chain git branch pointers (commit heads)",
		Version:     ProtocolVersion,
	}
}
