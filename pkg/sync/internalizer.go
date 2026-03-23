package sync

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/indexer"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

// Internalizer converts BEEF transactions into wallet.InternalizeAction calls.
type Internalizer struct {
	wallet  sdk.Interface
	indexer *indexer.IngestCtx
	logger  *slog.Logger
}

func NewInternalizer(wallet sdk.Interface, idx *indexer.IngestCtx, logger *slog.Logger) *Internalizer {
	return &Internalizer{
		wallet:  wallet,
		indexer: idx,
		logger:  logger,
	}
}

// FromMessage internalizes a payment received via message box.
func (i *Internalizer) FromMessage(ctx context.Context, msg *PaymentMessage) error {
	beefBytes, err := hex.DecodeString(msg.Beef)
	if err != nil {
		return fmt.Errorf("decode beef hex: %w", err)
	}

	prefixBytes, err := base64.StdEncoding.DecodeString(msg.DerivationPrefix)
	if err != nil {
		return fmt.Errorf("decode derivation prefix: %w", err)
	}

	suffixBytes, err := base64.StdEncoding.DecodeString(msg.DerivationSuffix)
	if err != nil {
		return fmt.Errorf("decode derivation suffix: %w", err)
	}

	senderKey, err := ec.PublicKeyFromString(msg.SenderIdentityKey)
	if err != nil {
		return fmt.Errorf("parse sender identity key: %w", err)
	}

	args := sdk.InternalizeActionArgs{
		Tx:          beefBytes,
		Description: "Payment from " + msg.Alias,
		Outputs: []sdk.InternalizeOutput{{
			OutputIndex: msg.OutputIndex,
			Protocol:    sdk.InternalizeProtocolWalletPayment,
			PaymentRemittance: &sdk.Payment{
				DerivationPrefix:  prefixBytes,
				DerivationSuffix:  suffixBytes,
				SenderIdentityKey: senderKey,
			},
		}},
	}

	if _, err := i.wallet.InternalizeAction(ctx, args, ""); err != nil {
		return fmt.Errorf("internalize message payment: %w", err)
	}

	i.logger.Info("internalized message payment",
		"alias", msg.Alias,
		"outputIndex", msg.OutputIndex,
		"satoshis", msg.Satoshis,
	)
	return nil
}

// outputClassification maps indexed events to wallet internalization parameters.
type outputClassification struct {
	basket   string
	protocol sdk.InternalizeProtocol
	tags     []string
}

// classifyOutput determines the basket, protocol, and tags from indexed events.
func classifyOutput(events []string, satoshis uint64) *outputClassification {
	hasEvent := func(prefix string) bool {
		for _, ev := range events {
			if strings.HasPrefix(ev, prefix) {
				return true
			}
		}
		return false
	}

	// BSV21 token
	if hasEvent("bsv21:") {
		return &outputClassification{
			basket:   "bsv21",
			protocol: sdk.InternalizeProtocolBasketInsertion,
			tags:     filterEvents(events, "bsv21:", "amt:", "sym:", "icon:", "dec:"),
		}
	}

	// OPNS name
	if hasEvent("opns:") {
		return &outputClassification{
			basket:   "opns",
			protocol: sdk.InternalizeProtocolBasketInsertion,
			tags:     filterEvents(events, "name:"),
		}
	}

	// Lock contract
	if hasEvent("lock:") {
		return &outputClassification{
			basket:   "lock",
			protocol: sdk.InternalizeProtocolBasketInsertion,
			tags:     filterEvents(events, "lock:"),
		}
	}

	// OrdLock listing
	if hasEvent("ordlock") {
		return &outputClassification{
			basket:   "ordlock",
			protocol: sdk.InternalizeProtocolBasketInsertion,
			tags:     filterEvents(events, "ordlock"),
		}
	}

	// Ordinal (has origin event)
	if hasEvent("origin:") {
		return &outputClassification{
			basket:   "1sat",
			protocol: sdk.InternalizeProtocolBasketInsertion,
			tags:     filterEvents(events, "origin:", "type:", "name:"),
		}
	}

	// Standard P2PKH funding (>1 sat)
	if hasEvent("p2pkh:") && satoshis > 1 {
		return &outputClassification{
			protocol: sdk.InternalizeProtocolWalletPayment,
		}
	}

	return nil
}

// filterEvents returns events matching any of the given prefixes.
func filterEvents(events []string, prefixes ...string) []string {
	var filtered []string
	for _, ev := range events {
		for _, prefix := range prefixes {
			if strings.HasPrefix(ev, prefix) {
				filtered = append(filtered, ev)
				break
			}
		}
	}
	return filtered
}

// FromSync internalizes outputs discovered via address sync. It runs the full
// parse pipeline on the transaction (including origin detection and ORDFS
// resolution) and matches owners against the provided derivation map.
//
// Returns true if any outputs were internalized, false if none matched.
func (i *Internalizer) FromSync(ctx context.Context, beef []byte, txid string, outputs []SyncOutput, derivations map[string]AddressDerivation) (bool, error) {
	tx, err := transaction.NewTransactionFromBEEF(beef)
	if err != nil {
		return false, fmt.Errorf("parse BEEF for %s: %w", txid, err)
	}

	idxCtx, err := i.indexer.ParseTx(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("parse transaction %s: %w", txid, err)
	}

	// Build a set of vouts we care about from the sync outputs
	voutSet := make(map[uint32]bool, len(outputs))
	for _, output := range outputs {
		vout, err := voutFromOutpoint(output.Outpoint)
		if err != nil {
			i.logger.Warn("skip output with bad outpoint", "outpoint", output.Outpoint, "err", err)
			continue
		}
		voutSet[vout] = true
	}

	var matched []sdk.InternalizeOutput
	for vout, idxOutput := range idxCtx.Outputs {
		if idxOutput == nil || !voutSet[uint32(vout)] {
			continue
		}

		// Match owners against derivation map
		var deriv *AddressDerivation
		for _, owner := range idxOutput.Owners {
			addr := owner.Address()
			if d, ok := derivations[addr]; ok {
				deriv = &d
				break
			}
		}
		if deriv == nil {
			continue
		}

		prefixBytes, err := base64.StdEncoding.DecodeString(deriv.DerivationPrefix)
		if err != nil {
			i.logger.Warn("decode derivation prefix", "address", deriv.Address, "err", err)
			continue
		}

		suffixBytes, err := base64.StdEncoding.DecodeString(deriv.DerivationSuffix)
		if err != nil {
			i.logger.Warn("decode derivation suffix", "address", deriv.Address, "err", err)
			continue
		}

		sats := uint64(0)
		if idxOutput.Satoshis != nil {
			sats = *idxOutput.Satoshis
		}

		class := classifyOutput(idxOutput.Events, sats)
		if class == nil {
			continue
		}

		out := sdk.InternalizeOutput{
			OutputIndex: uint32(vout),
			Protocol:    class.protocol,
		}

		if class.protocol == sdk.InternalizeProtocolWalletPayment {
			out.PaymentRemittance = &sdk.Payment{
				DerivationPrefix: prefixBytes,
				DerivationSuffix: suffixBytes,
			}
		} else {
			out.InsertionRemittance = &sdk.BasketInsertion{
				Basket: class.basket,
				Tags:   class.tags,
			}
		}

		matched = append(matched, out)
	}

	if len(matched) == 0 {
		return false, nil
	}

	args := sdk.InternalizeActionArgs{
		Tx:          beef,
		Description: "Synced outputs",
		Outputs:     matched,
	}

	if _, err := i.wallet.InternalizeAction(ctx, args, ""); err != nil {
		return false, fmt.Errorf("internalize synced outputs for %s: %w", txid, err)
	}

	i.logger.Info("internalized synced outputs", "txid", txid, "count", len(matched))
	return true, nil
}

// voutFromOutpoint extracts the output index from an outpoint string
// formatted as "txid_vout".
func voutFromOutpoint(outpoint string) (uint32, error) {
	parts := strings.Split(outpoint, "_")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid outpoint format %q: expected txid_vout", outpoint)
	}
	v, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse vout from %q: %w", outpoint, err)
	}
	return uint32(v), nil
}
