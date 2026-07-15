package ordfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// LoadByPointer loads content addressed by an OrdFS pointer string relative to base
// (for _N siblings). Supports _N, ord://, txid_vout / txid.vout, and bare txid.
func (o *Ordfs) LoadByPointer(ctx context.Context, base *transaction.Outpoint, pointer string, content bool) (*Response, error) {
	pointer = strings.TrimSpace(pointer)
	pointer = strings.TrimPrefix(pointer, "ord://")

	if vout, ok := parseRelativeVout(pointer); ok {
		if base == nil {
			return nil, fmt.Errorf("cannot resolve relative vout without base outpoint: %w", ErrNotFound)
		}
		return o.Load(ctx, &Request{
			Outpoint: &transaction.Outpoint{
				Txid:  base.Txid,
				Index: vout,
			},
			Content: content,
		})
	}

	outpoint, isTxid, err := resolvePointerToOutpoint(pointer)
	if err != nil {
		return nil, fmt.Errorf("invalid pointer: %w", err)
	}

	if isTxid {
		return o.Load(ctx, &Request{
			Txid:    &outpoint.Txid,
			Content: content,
		})
	}
	return o.Load(ctx, &Request{
		Outpoint: outpoint,
		Content:  content,
	})
}
