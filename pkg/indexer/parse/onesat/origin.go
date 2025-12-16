package onesat

import (
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/indexer/parse/bitcom"
	"github.com/b-open-io/1sat-stack/pkg/types"
)

const ORIGIN_TAG = "origin"

var TRIGGER = uint32(783968)

type Origin struct {
	Outpoint *types.Outpoint `json:"outpoint"`
	Nonce    uint64          `json:"nonce"`
	Parent   *types.Outpoint `json:"parent,omitempty"`
	Type     string          `json:"type,omitempty"`
	Map      bitcom.Map      `json:"map,omitempty"`
}

type OriginIndexer struct {
	indexer.BaseIndexer
}

func (i *OriginIndexer) Tag() string {
	return ORIGIN_TAG
}

func (i *OriginIndexer) Parse(idxCtx *indexer.IndexContext, vout uint32) any {
	output := idxCtx.Outputs[vout]
	outAcc := idxCtx.GetOutAcc(vout)

	if output.Satoshis != 1 || (idxCtx.Height < TRIGGER && idxCtx.Height != 0) {
		return nil
	}

	origin := &Origin{}
	if inscData, ok := output.Data[INSC_TAG]; ok {
		if insc, ok := inscData.(*Inscription); ok && insc.File != nil {
			origin.Type = insc.File.Type
		}
	}
	if mapData, ok := output.Data[bitcom.MAP_TAG]; ok {
		if mp, ok := mapData.(bitcom.Map); ok {
			origin.Map = mp
		}
	}

	satsIn := uint64(0)
	for spendIdx, spend := range idxCtx.Spends {
		if spend.Satoshis == 0 {
			break
		}
		spendOutpoint := indexer.OutpointToTypes(&spend.Outpoint)
		spendOutAcc := idxCtx.GetSpendOutAcc(spendIdx)
		_ = spendOutAcc // unused for now

		if satsIn == outAcc && spend.Satoshis == 1 && spend.BlockHeight >= TRIGGER {
			origin.Parent = spendOutpoint
			output.AddEvent(ORIGIN_TAG + ":parent:" + spendOutpoint.OrdinalString())
			if o, ok := spend.Data[ORIGIN_TAG]; ok {
				if parent, ok := o.(*Origin); ok {
					origin.Nonce = parent.Nonce + 1
					origin.Outpoint = parent.Outpoint
					if origin.Map == nil {
						origin.Map = parent.Map
					} else if parent.Map != nil {
						origin.Map = parent.Map.Merge(origin.Map)
					}
				}
			}
			break
		}
		satsIn += spend.Satoshis
		if satsIn > outAcc {
			origin.Outpoint = types.NewOutpointFromHash(idxCtx.Txid, vout)
			break
		}
	}

	var outpointStr string
	if origin.Outpoint != nil {
		outpointStr = origin.Outpoint.OrdinalString()
	}
	output.AddEvent(ORIGIN_TAG + ":outpoint:" + outpointStr)

	return origin
}
