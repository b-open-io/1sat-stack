package sync

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

// Internalizer converts BEEF transactions into wallet.InternalizeAction calls.
// It supports two modes: FromMessage for explicit payment messages with derivation
// info, and FromSync for matching transaction outputs against known address derivations.
type Internalizer struct {
	wallet sdk.Interface
	logger *slog.Logger
}

func NewInternalizer(wallet sdk.Interface, logger *slog.Logger) *Internalizer {
	return &Internalizer{
		wallet: wallet,
		logger: logger,
	}
}

// FromMessage internalizes a payment received via message box. The message
// contains explicit output index and derivation info from the sender.
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

// FromSync internalizes outputs discovered via address sync. It parses the BEEF
// to extract the transaction, derives addresses from output scripts, and matches
// them against the provided derivation map to build internalize outputs.
//
// The derivations map is keyed by address string. Each SyncOutput's outpoint is
// parsed to extract the vout, then the corresponding output script is checked
// for a P2PKH address match in the derivations map.
//
// Returns true if any outputs were internalized, false if none matched.
func (i *Internalizer) FromSync(ctx context.Context, beef []byte, txid string, outputs []SyncOutput, derivations map[string]AddressDerivation) (bool, error) {
	tx, err := transaction.NewTransactionFromBEEF(beef)
	if err != nil {
		return false, fmt.Errorf("parse BEEF for %s: %w", txid, err)
	}

	var matched []sdk.InternalizeOutput
	for _, output := range outputs {
		vout, err := voutFromOutpoint(output.Outpoint)
		if err != nil {
			i.logger.Warn("skip output with bad outpoint", "outpoint", output.Outpoint, "err", err)
			continue
		}

		if int(vout) >= len(tx.Outputs) {
			i.logger.Warn("vout exceeds transaction outputs", "outpoint", output.Outpoint, "vout", vout, "numOutputs", len(tx.Outputs))
			continue
		}

		lockingScript := tx.Outputs[vout].LockingScript
		if lockingScript == nil || !lockingScript.IsP2PKH() {
			continue
		}

		addr, err := lockingScript.Address()
		if err != nil {
			i.logger.Warn("extract address from output script", "outpoint", output.Outpoint, "err", err)
			continue
		}

		deriv, ok := derivations[addr.AddressString]
		if !ok {
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

		matched = append(matched, sdk.InternalizeOutput{
			OutputIndex: vout,
			Protocol:    sdk.InternalizeProtocolWalletPayment,
			PaymentRemittance: &sdk.Payment{
				DerivationPrefix: prefixBytes,
				DerivationSuffix: suffixBytes,
			},
		})
	}

	if len(matched) == 0 {
		return false, nil
	}

	args := sdk.InternalizeActionArgs{
		Tx:          beef,
		Description: "Synced payment",
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
