package ordfs

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OUTPOINT_BEEF is the BRC-158 envelope prefix (little-endian on the wire).
const OUTPOINT_BEEF = uint32(0x16a7beef)

// Provenance holds a BRC-150 tip→origin package (binary BEEF + path metadata).
type Provenance struct {
	Origin *transaction.Outpoint
	Tip    *transaction.Outpoint
	// Path is ordered tip → … → origin (inclusive).
	Path []*transaction.Outpoint
	// Beef is Outpoint BEEF (BRC-158) for the tip outpoint.
	Beef []byte
}

// BuildProvenance assembles a BRC-150 provenance package for a 1-sat tip outpoint.
// It resolves the ordinal chain (crawling if needed), loads each path transaction
// from beef storage, and returns Outpoint BEEF (BRC-158) for the tip.
func (o *Ordfs) BuildProvenance(ctx context.Context, tip *transaction.Outpoint) (*Provenance, error) {
	if tip == nil {
		return nil, fmt.Errorf("tip outpoint is required")
	}

	output, err := o.loadOutput(ctx, tip)
	if err != nil {
		return nil, fmt.Errorf("failed to load tip output: %w", err)
	}
	if output.Satoshis != 1 {
		return nil, fmt.Errorf("tip must be a 1-sat output: %w", ErrNotFound)
	}

	path, origin, err := o.provenancePath(ctx, tip)
	if err != nil {
		return nil, err
	}

	beefBytes, err := o.assemblePathBeef(ctx, tip, path)
	if err != nil {
		return nil, err
	}

	return &Provenance{
		Origin: origin,
		Tip:    tip,
		Path:   path,
		Beef:   beefBytes,
	}, nil
}

// provenancePath returns outpoints tip→origin inclusive and the origin.
func (o *Ordfs) provenancePath(ctx context.Context, tip *transaction.Outpoint) ([]*transaction.Outpoint, *transaction.Outpoint, error) {
	info, err := o.origins.GetOrigin(ctx, tip)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check origin: %w", err)
	}
	if info == nil {
		if _, err := o.backwardCrawl(ctx, tip); err != nil {
			return nil, nil, fmt.Errorf("backward crawl failed: %w", err)
		}
		info, err = o.origins.GetOrigin(ctx, tip)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to check origin after crawl: %w", err)
		}
		if info == nil {
			return nil, nil, fmt.Errorf("tip not indexed after crawl: %w", ErrNotFound)
		}
	}

	path := make([]*transaction.Outpoint, 0, info.Seq+1)
	for s := int(info.Seq); s >= 0; s-- {
		op, err := o.origins.GetSeqAt(ctx, info.Origin, uint32(s))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to lookup seq %d: %w", s, err)
		}
		if op == nil {
			return nil, nil, fmt.Errorf("missing chain entry at seq %d for origin %s: %w", s, info.Origin.String(), ErrNotFound)
		}
		path = append(path, op)
	}

	if !path[0].Txid.Equal(tip.Txid) || path[0].Index != tip.Index {
		return nil, nil, fmt.Errorf("path tip mismatch: got %s want %s", path[0].String(), tip.String())
	}
	last := path[len(path)-1]
	if !last.Txid.Equal(info.Origin.Txid) || last.Index != info.Origin.Index {
		return nil, nil, fmt.Errorf("path origin mismatch: got %s want %s", last.String(), info.Origin.String())
	}

	return path, info.Origin, nil
}

// assemblePathBeef merges BEEF for every path hop and, for each hop, source
// txs for inputs[0..carrier] only. Later inputs are irrelevant to 1Sat ordinal
// assignment and would explode proof size on fat multi-in transfers.
// Returns Outpoint BEEF (BRC-158) for the tip outpoint.
func (o *Ordfs) assemblePathBeef(ctx context.Context, tip *transaction.Outpoint, path []*transaction.Outpoint) ([]byte, error) {
	var merged *transaction.Beef
	seen := make(map[chainhash.Hash]struct{}, len(path)*2)

	mergeTxid := func(txid *chainhash.Hash) error {
		if txid == nil {
			return nil
		}
		if _, ok := seen[*txid]; ok {
			return nil
		}
		seen[*txid] = struct{}{}
		b, err := o.beef.LoadBeef(ctx, txid)
		if err != nil {
			return fmt.Errorf("failed to load beef for %s: %w", txid.String(), err)
		}
		if merged == nil {
			merged = b
			return nil
		}
		if err := merged.MergeBeef(b); err != nil {
			return fmt.Errorf("failed to merge beef for %s: %w", txid.String(), err)
		}
		return nil
	}

	for i, op := range path {
		if err := mergeTxid(&op.Txid); err != nil {
			return nil, err
		}
		tx := merged.FindTransactionForSigningByHash(&op.Txid)
		if tx == nil {
			return nil, fmt.Errorf("transaction %s missing after merge", op.Txid.String())
		}

		carrier, err := o.carrierInputIndex(ctx, tx, op, pathParent(path, i))
		if err != nil {
			return nil, fmt.Errorf("carrier input for %s: %w", op.String(), err)
		}
		for j := 0; j <= carrier; j++ {
			in := tx.Inputs[j]
			if in.SourceTXID == nil {
				continue
			}
			if err := mergeTxid(in.SourceTXID); err != nil {
				return nil, fmt.Errorf("input %d of %s: %w", j, op.Txid.String(), err)
			}
		}
	}

	if merged == nil {
		return nil, fmt.Errorf("no transactions on path: %w", ErrNotFound)
	}

	beefBytes, err := outpointBeefBytes(merged, tip)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize outpoint beef: %w", err)
	}
	return beefBytes, nil
}

// outpointBeefBytes encodes BRC-158: 0x16a7beef || outpoint(36) || BEEF.
func outpointBeefBytes(b *transaction.Beef, subject *transaction.Outpoint) ([]byte, error) {
	if b == nil || subject == nil {
		return nil, fmt.Errorf("beef and subject outpoint are required")
	}
	body, err := b.Bytes()
	if err != nil {
		return nil, err
	}
	op := subject.Bytes()
	out := make([]byte, 4+len(op)+len(body))
	binary.LittleEndian.PutUint32(out[0:4], OUTPOINT_BEEF)
	copy(out[4:4+len(op)], op)
	copy(out[4+len(op):], body)
	return out, nil
}

// pathParent is the next hop toward origin (path is tip→origin), or nil at origin.
func pathParent(path []*transaction.Outpoint, i int) *transaction.Outpoint {
	if i+1 >= len(path) {
		return nil
	}
	return path[i+1]
}

// carrierInputIndex is the input that supplies the ordinal for hopOut.
// When parent is known (transfer hop), match that spend; otherwise use 1Sat
// offset math (origin hop).
func (o *Ordfs) carrierInputIndex(ctx context.Context, tx *transaction.Transaction, hopOut, parent *transaction.Outpoint) (int, error) {
	if hopOut == nil || int(hopOut.Index) >= len(tx.Outputs) {
		return -1, fmt.Errorf("invalid hop outpoint")
	}
	if tx.Outputs[hopOut.Index].Satoshis != 1 {
		return -1, fmt.Errorf("hop output is not 1-sat")
	}

	if parent != nil {
		for j, in := range tx.Inputs {
			if in.SourceTXID != nil && in.SourceTXID.Equal(parent.Txid) && in.SourceTxOutIndex == parent.Index {
				return j, nil
			}
		}
		return -1, fmt.Errorf("parent %s not spent by %s", parent.String(), hopOut.Txid.String())
	}

	var ordinalOffset uint64
	for i := 0; i < int(hopOut.Index); i++ {
		if tx.Outputs[i].Satoshis > 0 {
			ordinalOffset += tx.Outputs[i].Satoshis
		}
	}

	var cumulative uint64
	for j, in := range tx.Inputs {
		if in.SourceTXID == nil {
			return -1, fmt.Errorf("input %d missing source txid", j)
		}
		prevOut, err := o.loadOutput(ctx, &transaction.Outpoint{
			Txid:  *in.SourceTXID,
			Index: in.SourceTxOutIndex,
		})
		if err != nil {
			return -1, fmt.Errorf("load input %d: %w", j, err)
		}
		if cumulative == ordinalOffset {
			return j, nil
		}
		cumulative += prevOut.Satoshis
		if cumulative > ordinalOffset {
			// Ordinal carved from mid multi-sat input — that input is still the carrier.
			return j, nil
		}
	}
	return -1, fmt.Errorf("no carrier input for %s", hopOut.String())
}
