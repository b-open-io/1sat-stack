package bitcom

import (
	"crypto/sha256"

	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/script"
)

var B_PROTO = "19HxigV4QyBv3tHpQVcUEQyq1pzZVdoAut"

type BIndexer struct {
	indexer.BaseIndexer
}

func (i *BIndexer) Tag() string {
	return "b"
}

func (i *BIndexer) Parse(idxCtx *indexer.IndexContext, vout uint32) any {
	output := idxCtx.Outputs[vout]
	if bitcomData, ok := output.Data[BITCOM_TAG]; ok {
		for _, b := range bitcomData.([]*Bitcom) {
			if b.Protocol == B_PROTO {
				b := ParseB(script.NewFromBytes(b.Script), 0)
				if b != nil {
					return b
				}
			}
		}
	}
	return nil
}

func ParseB(scr *script.Script, idx int) (b *types.File) {
	pos := &idx
	b = &types.File{}
	for i := 0; i < 4; i++ {
		prevIdx := *pos
		op, err := scr.ReadOp(pos)
		if err != nil || op.Op == script.OpRETURN || (op.Op == 1 && op.Data[0] == '|') {
			*pos = prevIdx
			break
		}

		switch i {
		case 0:
			b.Content = op.Data
		case 1:
			b.Type = string(op.Data)
		case 2:
			b.Encoding = string(op.Data)
		case 3:
			b.Name = string(op.Data)
		}
	}
	hash := sha256.Sum256(b.Content)
	b.Size = uint32(len(b.Content))
	b.Hash = hash[:]
	return
}
