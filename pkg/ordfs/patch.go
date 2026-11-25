package ordfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/go-deltasync/vcdiff"
)

const (
	patchVersion   = 0x00
	maxPatchDepth  = 8
	patchHeaderLen = 1 + outpointSize
)

var (
	errInvalidPatch = errors.New("invalid patch")
	errPatchTooDeep = errors.New("patch chain too deep")
)

type patchRecord struct {
	Base  *transaction.Outpoint
	Delta []byte
}

func parsePatch(b []byte) (*patchRecord, error) {
	if len(b) < patchHeaderLen {
		return nil, errInvalidPatch
	}
	if b[0] != patchVersion {
		return nil, errInvalidPatch
	}
	op := transaction.NewOutpointFromBytes(b[1:patchHeaderLen])
	if op == nil {
		return nil, errInvalidPatch
	}
	return &patchRecord{Base: op, Delta: b[patchHeaderLen:]}, nil
}

func applyVCDIFF(source, delta []byte) ([]byte, error) {
	var out bytes.Buffer
	if _, err := vcdiff.Decode(source, bytes.NewReader(delta), &out); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidPatch, err)
	}
	return out.Bytes(), nil
}

func (o *Ordfs) resolvePatch(ctx context.Context, content []byte, depth int) (*Response, error) {
	if depth >= maxPatchDepth {
		return nil, errPatchTooDeep
	}
	rec, err := parsePatch(content)
	if err != nil {
		return nil, err
	}
	base, err := o.Load(ctx, &Request{Outpoint: rec.Base, Content: true})
	if err != nil {
		return nil, err
	}
	source := base.Content
	sourceType := base.ContentType
	if isPatchType(base.ContentType) {
		resolved, err := o.resolvePatch(ctx, base.Content, depth+1)
		if err != nil {
			return nil, err
		}
		source = resolved.Content
		sourceType = resolved.ContentType
	}
	out, err := applyVCDIFF(source, rec.Delta)
	if err != nil {
		return nil, err
	}
	return &Response{
		ContentType:   sourceType,
		Content:       out,
		ContentLength: len(out),
	}, nil
}
