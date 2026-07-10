package bsv21

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type Op string

var (
	OpDeployMint Op = "deploy+mint"
	OpDeployAuth Op = "deploy+auth"
	OpMint       Op = "mint"
	OpAuth       Op = "auth"
	OpTransfer   Op = "transfer"
	OpBurn       Op = "burn"
)

type Bsv21 struct {
	Id       string                   `json:"id,omitempty"`
	Op       string                   `json:"op"`
	Symbol   *string                  `json:"sym,omitempty"`
	Decimals *uint8                   `json:"dec,omitempty,string"`
	Icon     *string                  `json:"icon,omitempty"`
	Amt      uint64                   `json:"amt,string"`
	Insc     *inscription.Inscription `json:"-"`
}

func Decode(scr *script.Script) *Bsv21 {
	insc := inscription.Decode(scr)
	data := map[string]any{}
	if insc == nil {
		return nil
	} else if insc.File.Type != "application/bsv-20" {
		return nil
	} else if err := json.Unmarshal(insc.File.Content, &data); err != nil {
		return nil
	} else if p, ok := data["p"]; !ok || p != "bsv-20" {
		return nil
	} else {
		bsv21 := &Bsv21{
			Insc: insc,
		}
		if op, ok := data["op"]; ok {
			bsv21.Op = strings.ToLower(op.(string))
		} else {
			return nil
		}

		switch bsv21.Op {
		case string(OpAuth), string(OpDeployAuth):
			if _, ok := data["amt"]; ok {
				return nil
			}
		default:
			if amt, ok := data["amt"]; !ok {
				return nil
			} else if amtStr, ok := amt.(string); !ok {
				return nil
			} else if bsv21.Amt, err = strconv.ParseUint(amtStr, 10, 64); err != nil {
				return nil
			}

		}

		if dec, ok := data["dec"]; ok {
			decStr, ok := dec.(string)
			if !ok {
				return nil
			}
			var val uint64
			if val, err = strconv.ParseUint(decStr, 10, 8); err != nil || val > 18 {
				return nil
			}
			decimals := uint8(val)
			bsv21.Decimals = &decimals
		}

		switch bsv21.Op {
		case string(OpDeployMint), string(OpDeployAuth):
			if sym, ok := data["sym"]; ok {
				s := sym.(string)
				bsv21.Symbol = &s
			}
			if icon, ok := data["icon"]; ok {
				i := icon.(string)
				bsv21.Icon = &i
			}
		case string(OpMint), string(OpAuth), string(OpTransfer), string(OpBurn):
			if id, ok := data["id"]; !ok {
				return nil
			} else {
				// Validate that the id is a properly formatted outpoint
				if _, err := transaction.OutpointFromString(id.(string)); err != nil {
					return nil // Invalid outpoint format, not a valid BSV21 token
				}
				bsv21.Id = id.(string)
			}
		default:
			return nil
		}
		return bsv21
	}
}

func (b *Bsv21) Lock(lockingScript *script.Script) (*script.Script, error) {
	if j, err := json.Marshal(b); err != nil {
		return nil, err
	} else {
		insc := &inscription.Inscription{
			File: inscription.File{
				Content: j,
				Type:    "application/bsv-20",
			},
			ScriptSuffix: *lockingScript,
		}
		return insc.Lock()
	}
}
