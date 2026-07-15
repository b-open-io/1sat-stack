package ordfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OrdfsRefParam is the media-type parameter marking a content reference.
// Example: image/png; ref=ordfs
const OrdfsRefParam = "ref=ordfs"

// IsContentRef reports whether contentType includes the ref=ordfs parameter.
func IsContentRef(contentType string) bool {
	parts := strings.Split(contentType, ";")
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		if p == OrdfsRefParam {
			return true
		}
		key, val, ok := strings.Cut(p, "=")
		if ok && strings.TrimSpace(key) == "ref" && strings.TrimSpace(val) == "ordfs" {
			return true
		}
	}
	return false
}

// ResolveContentRef replaces Content, ContentType, and ContentLength with the
// source inscription when resp is a content ref (ref=ordfs). One hop only.
// Outpoint, Origin, Sequence, Map, and Parent are left unchanged.
func (o *Ordfs) ResolveContentRef(ctx context.Context, resp *Response) (*Response, error) {
	if resp == nil || !IsContentRef(resp.ContentType) {
		return resp, nil
	}

	pointer := strings.TrimSpace(string(resp.Content))
	if pointer == "" {
		return nil, fmt.Errorf("empty content ref pointer: %w", ErrNotFound)
	}
	pointer = strings.TrimPrefix(pointer, "ord://")

	source, err := o.LoadByPointer(ctx, resp.Outpoint, pointer, true)
	if err != nil {
		return nil, err
	}

	resp.Content = source.Content
	resp.ContentType = source.ContentType
	resp.ContentLength = source.ContentLength
	return resp, nil
}

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
		return nil, fmt.Errorf("invalid content ref pointer: %w", err)
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
