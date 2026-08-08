package ordfs

import (
	"context"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Provenance holds a BRC-150 tip→origin package (binary BEEF + path metadata).
type Provenance struct {
	Origin *transaction.Outpoint
	Tip    *transaction.Outpoint
	// Path is ordered tip → … → origin (inclusive).
	Path []*transaction.Outpoint
	// Beef is AtomicBEEF rooted at the tip txid (BRC-95), covering path transactions.
	Beef []byte
}

// BuildProvenance assembles a BRC-150 provenance package for a 1-sat tip outpoint.
// It resolves the ordinal chain (crawling if needed), loads each path transaction
// from beef storage, and returns AtomicBEEF rooted at the tip.
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

// assemblePathBeef merges BEEF for every path hop and, for each hop, every
// input’s source transaction. Source txs are required so a verifier can re-run
// 1Sat ordinal assignment (path “spends parent” alone is not enough on multi-in
// transfers). Returns AtomicBEEF rooted at the tip.
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

	for _, op := range path {
		if err := mergeTxid(&op.Txid); err != nil {
			return nil, err
		}
		tx := merged.FindTransactionForSigningByHash(&op.Txid)
		if tx == nil {
			return nil, fmt.Errorf("transaction %s missing after merge", op.Txid.String())
		}
		// Always attach input source txs (even when hop is mined) for ordinal math.
		for _, input := range tx.Inputs {
			if input.SourceTXID == nil {
				continue
			}
			if err := mergeTxid(input.SourceTXID); err != nil {
				return nil, fmt.Errorf("input of %s: %w", op.Txid.String(), err)
			}
		}
	}

	if merged == nil {
		return nil, fmt.Errorf("no transactions on path: %w", ErrNotFound)
	}

	beefBytes, err := merged.AtomicBytes(&tip.Txid)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize atomic beef: %w", err)
	}
	return beefBytes, nil
}
