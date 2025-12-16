package bsv21

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/bsv21/ltm"
	"github.com/bitcoin-sv/go-templates/template/bsv21/pow20"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

// Lookup implements the LookupService interface for BSV21
type Lookup struct {
	storage   *txo.OutputStore
	mintCache sync.Map // Cache of mint tokens by tokenId
}

// NewLookup creates a new BSV21 lookup service
func NewLookup(storage *txo.OutputStore) *Lookup {
	return &Lookup{
		storage: storage,
	}
}

// OutputAdmittedByTopic is called when an output is admitted to a topic
func (l *Lookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}

	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	// Decode BSV21 data
	b := bsv21.Decode(tx.Outputs[int(payload.OutputIndex)].LockingScript)
	if b == nil {
		return nil
	}

	events := make([]string, 0, 5)

	// For mint operations, set the ID to the ordinal string
	if b.Op == string(bsv21.OpMint) {
		b.Id = outpoint.OrdinalString()
		if b.Symbol != nil {
			events = append(events, fmt.Sprintf("sym:%s", *b.Symbol))
		}
	} else if b.Op == string(bsv21.OpTransfer) {
		// For transfers, get the mint token data (uses cache)
		tokenOutpoint, err := transaction.OutpointFromString(b.Id)
		if err == nil {
			mintData, err := l.GetToken(ctx, tokenOutpoint)
			if err == nil {
				if sym, ok := mintData["sym"].(string); ok {
					b.Symbol = &sym
				}
				if dec, ok := mintData["dec"].(uint8); ok {
					b.Decimals = &dec
				} else if dec, ok := mintData["dec"].(float64); ok {
					decUint8 := uint8(dec)
					b.Decimals = &decUint8
				}
				if icon, ok := mintData["icon"].(string); ok {
					b.Icon = &icon
				}
			}
		}
	}
	events = append(events, fmt.Sprintf("id:%s", b.Id))

	// Extract address from suffix script
	var address string
	suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
	if p := p2pkh.Decode(suffix, true); p != nil {
		address = p.AddressString
		events = append(events, fmt.Sprintf("p2pkh:%s:%s", address, b.Id))
	} else if c := cosign.Decode(suffix); c != nil {
		address = c.Address
		events = append(events, fmt.Sprintf("cos:%s:%s", address, b.Id))
	} else if ltmData := ltm.Decode(suffix); ltmData != nil {
		events = append(events, fmt.Sprintf("ltm:%s", b.Id))
	} else if pow20Data := pow20.Decode(suffix); pow20Data != nil {
		events = append(events, fmt.Sprintf("pow20:%s", b.Id))
	} else if ordLock := ordlock.Decode(suffix); ordLock != nil {
		if ordLock.Seller != nil {
			address = ordLock.Seller.AddressString
			events = append(events, fmt.Sprintf("list:%s:%s", address, b.Id))
		}
		events = append(events, fmt.Sprintf("list:%s", b.Id))
	}

	// Build BSV21 data structure
	bsv21Data := map[string]interface{}{
		"id":  b.Id,
		"op":  b.Op,
		"amt": strconv.FormatUint(b.Amt, 10),
	}

	if b.Symbol != nil {
		bsv21Data["sym"] = *b.Symbol
	}
	if b.Decimals != nil {
		bsv21Data["dec"] = *b.Decimals
	}
	if b.Icon != nil {
		bsv21Data["icon"] = *b.Icon
	}
	if address != "" {
		bsv21Data["address"] = address
	}

	dataToStore := map[string]any{
		"bsv21": bsv21Data,
	}

	// Save events with data using timestamp-based score (not HeightScore)
	score := float64(time.Now().UnixNano())
	return l.storage.SaveEvents(ctx, outpoint, events, dataToStore, score)
}

// GetDocumentation returns documentation for this lookup service
func (l *Lookup) GetDocumentation() string {
	return "BSV21 Lookup Service"
}

// GetMetaData returns metadata for this lookup service
func (l *Lookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}

// OutputSpent is called when a previously-admitted UTXO is spent
func (l *Lookup) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required
func (l *Lookup) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted permanently removes the UTXO from all indices
func (l *Lookup) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated
func (l *Lookup) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries
func (l *Lookup) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{
		Type: lookup.AnswerTypeFormula,
	}, nil
}

// GetBalance calculates the total balance of BSV21 tokens for given event patterns
func (l *Lookup) GetBalance(ctx context.Context, events []string) (uint64, int, error) {
	keys := make([][]byte, len(events))
	for i, event := range events {
		keys[i] = []byte(event)
	}

	cfg := txo.NewOutputSearchCfg().
		WithKeys(keys...).
		WithFilterSpent(true).
		WithTags("bsv21")

	// Search for unspent outputs only
	outputs, err := l.storage.SearchOutputs(ctx, cfg)
	if err != nil {
		return 0, 0, err
	}

	var totalBalance uint64
	count := 0

	for _, output := range outputs {
		if output == nil || output.Data == nil {
			continue
		}

		dataMap, ok := output.Data["bsv21"]
		if !ok {
			continue
		}

		bsv21Data, ok := dataMap.(map[string]interface{})
		if !ok {
			continue
		}

		amtStr, ok := bsv21Data["amt"].(string)
		if !ok {
			continue
		}

		amt, err := strconv.ParseUint(amtStr, 10, 64)
		if err != nil {
			continue
		}

		totalBalance += amt
		count++
	}

	return totalBalance, count, nil
}

// GetToken returns the mint transaction details for a specific BSV21 token
func (l *Lookup) GetToken(ctx context.Context, outpoint *transaction.Outpoint) (map[string]interface{}, error) {
	tokenId := outpoint.OrdinalString()

	// Check cache first
	if cached, ok := l.mintCache.Load(tokenId); ok {
		return cached.(map[string]interface{}), nil
	}

	// Load output data for this outpoint
	cfg := txo.NewOutputSearchCfg().WithTags("bsv21")
	output, err := l.storage.LoadOutput(ctx, outpoint, cfg)
	if err != nil {
		return nil, err
	}

	if output == nil {
		return nil, fmt.Errorf("token data not found")
	}

	bsv21DataRaw, ok := output.Data["bsv21"]
	if !ok {
		return nil, fmt.Errorf("token data not found")
	}

	bsv21Data, ok := bsv21DataRaw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid token data format")
	}

	op, ok := bsv21Data["op"].(string)
	if !ok || op != "deploy+mint" {
		return nil, fmt.Errorf("outpoint exists but is not a mint transaction (op=%s)", op)
	}

	response := map[string]interface{}{
		"id":   tokenId,
		"txid": outpoint.Txid.String(),
		"vout": outpoint.Index,
		"op":   op,
	}

	if amtStr, ok := bsv21Data["amt"].(string); ok {
		response["amt"] = amtStr
	}

	if sym, ok := bsv21Data["sym"].(string); ok {
		response["sym"] = sym
	}

	if dec, ok := bsv21Data["dec"].(float64); ok {
		response["dec"] = uint8(dec)
	}

	if icon, ok := bsv21Data["icon"].(string); ok {
		response["icon"] = icon
	}

	// Cache the response
	l.mintCache.Store(tokenId, response)

	return response, nil
}
