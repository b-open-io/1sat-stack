package bsv21

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// BSV21 tag constants
const (
	Tag = "bsv21"
)

// Event types
const (
	IssueEvent   = "iss"
	IdEvent      = "id"
	ValidEvent   = "val"
	InvalidEvent = "inv"
	PendingEvent = "pen"
)

// Status constants
type Status int

const (
	Pending Status = iota
	Valid
	Invalid
)

// IndexingFee is the fee required for indexing
const IndexingFee = 1000

// Bsv21 represents BSV21 token data
type Bsv21 struct {
	Id          string  `json:"id,omitempty"`
	Op          string  `json:"op"`
	Symbol      *string `json:"sym,omitempty"`
	Decimals    uint8   `json:"dec"`
	Icon        string  `json:"icon,omitempty"`
	Amt         uint64  `json:"amt"`
	Status      Status  `json:"status"`
	Reason      *string `json:"reason,omitempty"`
	FundAddress string  `json:"fundAddress,omitempty"`
	FundBalance int     `json:"-"`
}

// Indexer implements the indexer.Indexer interface for BSV21 tokens
type Indexer struct {
	indexer.BaseIndexer
	WhitelistFn func(tokenId string) bool
	BlacklistFn func(tokenId string) bool
	Network     types.Network
	Logger      *slog.Logger
}

// NewIndexer creates a new BSV21 indexer
func NewIndexer(network types.Network) *Indexer {
	return &Indexer{
		Network: network,
		Logger:  slog.Default(),
	}
}

// Tag returns the unique identifier for this indexer
func (i *Indexer) Tag() string {
	return Tag
}

// Parse parses a transaction output for BSV21 data
func (i *Indexer) Parse(idxCtx *indexer.IndexContext, vout uint32) any {
	output := idxCtx.Outputs[vout]

	// Check for inscription data (requires inscription indexer to have run first)
	inscData, ok := output.Data["insc"]
	if !ok {
		return nil
	}

	// Try to extract inscription as map
	insc, ok := inscData.(map[string]interface{})
	if !ok {
		return nil
	}

	// Check for BSV21 content type
	fileData, ok := insc["file"].(map[string]interface{})
	if !ok {
		return nil
	}
	contentType, _ := fileData["type"].(string)
	if contentType != "application/bsv-20" {
		return nil
	}

	// Get JSON map
	jsonMap, ok := insc["json"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Check protocol
	protocol, _ := jsonMap["p"].(string)
	if protocol != "bsv-20" {
		return nil
	}

	bsv21 := &Bsv21{}

	// Parse operation
	if op, ok := jsonMap["op"].(string); ok {
		bsv21.Op = strings.ToLower(op)
	} else {
		return nil
	}

	// Parse amount
	if amt, ok := jsonMap["amt"].(string); ok {
		var err error
		if bsv21.Amt, err = strconv.ParseUint(amt, 10, 64); err != nil {
			return nil
		}
	}

	// Parse decimals
	if dec, ok := jsonMap["dec"].(string); ok {
		val, err := strconv.ParseUint(dec, 10, 8)
		if err != nil || val > 18 {
			return nil
		}
		bsv21.Decimals = uint8(val)
	}

	switch bsv21.Op {
	case "deploy+mint":
		bsv21.Id = output.Outpoint.String()
		if sym, ok := jsonMap["sym"].(string); ok {
			bsv21.Symbol = &sym
		}
		bsv21.Status = Valid
		if icon, ok := jsonMap["icon"].(string); ok {
			if strings.HasPrefix(icon, "_") {
				icon = output.Outpoint.Txid.String() + icon
			}
			bsv21.Icon = icon
		}

		output.AddEvent(Tag + ":" + IssueEvent)

	case "transfer", "burn":
		id, ok := jsonMap["id"].(string)
		if !ok {
			return nil
		}
		if _, err := transaction.OutpointFromString(id); err != nil {
			return nil
		}
		bsv21.Id = id

	default:
		return nil
	}

	output.AddEvent(Tag + ":" + IdEvent + ":" + bsv21.Id)
	return bsv21
}

// bsv21Token holds aggregated token data during PreSave
type bsv21Token struct {
	balance uint64
	reason  *string
	token   *Bsv21
	outputs []*txo.IndexedOutput
}

// PreSave validates token transfers after all outputs are parsed
func (i *Indexer) PreSave(idxCtx *indexer.IndexContext) {
	tokens := make(map[string]*bsv21Token)

	isPending := false
	for _, output := range idxCtx.Outputs {
		bsv21Data, ok := output.Data[Tag]
		if !ok {
			continue
		}
		bsv21, ok := bsv21Data.(*Bsv21)
		if !ok {
			continue
		}
		if bsv21.Op == "deploy+mint" {
			continue
		}
		if token, ok := tokens[bsv21.Id]; !ok {
			tokens[bsv21.Id] = &bsv21Token{
				outputs: []*txo.IndexedOutput{output},
			}
		} else {
			token.outputs = append(token.outputs, output)
		}
	}

	if len(tokens) == 0 {
		return
	}

	// Process spends to tally input balances
	for _, spend := range idxCtx.Spends {
		bsv21Data, ok := spend.Data[Tag]
		if !ok {
			continue
		}
		bsv21, ok := bsv21Data.(*Bsv21)
		if !ok {
			continue
		}
		if bsv21.Status == Pending {
			isPending = true
			break
		} else if bsv21.Status == Valid {
			if token, ok := tokens[bsv21.Id]; ok {
				token.balance += bsv21.Amt
				token.token = bsv21
			}
		}
	}

	reasons := make(map[string]string)
	if !isPending {
		for _, token := range tokens {
			for _, output := range token.outputs {
				bsv21Data, ok := output.Data[Tag]
				if !ok {
					continue
				}
				bsv21, ok := bsv21Data.(*Bsv21)
				if !ok {
					continue
				}
				if token.token == nil {
					reason := "missing inputs"
					token.reason = &reason
				} else if bsv21.Amt > token.balance {
					reason := "insufficient funds"
					token.reason = &reason
				} else {
					bsv21.Icon = token.token.Icon
					bsv21.Symbol = token.token.Symbol
					bsv21.Decimals = token.token.Decimals
					bsv21.FundAddress = token.token.FundAddress
					token.balance -= bsv21.Amt
				}
			}
		}
	}

	// Set final status and events
	for tokenId, token := range tokens {
		for _, output := range token.outputs {
			bsv21Data, ok := output.Data[Tag]
			if !ok {
				continue
			}
			bsv21, ok := bsv21Data.(*Bsv21)
			if !ok {
				continue
			}
			if isPending {
				output.AddEvent(Tag + ":" + PendingEvent + ":" + tokenId)
			} else if reason, ok := reasons[tokenId]; ok {
				bsv21.Status = Invalid
				bsv21.Reason = &reason
				output.AddEvent(Tag + ":" + InvalidEvent + ":" + tokenId)
			} else {
				bsv21.Status = Valid
				output.AddEvent(Tag + ":" + ValidEvent + ":" + tokenId)
			}
		}
	}
}
