package ordfs

import (
	"context"
	"fmt"

	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/b-open-io/1sat-stack/pkg/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const maxOwnershipChainLength = 10000

// OwnershipEntry records the controlling P2PKH address at one point in an
// ordinal's spend chain.
type OwnershipEntry struct {
	Outpoint transaction.Outpoint
	Address  string
}

// OwnershipChain walks an ordinal forward from its root through all known
// spends. Callers can combine the returned outpoints with their indexed block
// positions to select the controller at any historical point in time.
func (o *Ordfs) OwnershipChain(ctx context.Context, root *transaction.Outpoint) ([]OwnershipEntry, error) {
	if root == nil {
		return nil, fmt.Errorf("collection root is required")
	}

	current := root
	seen := make(map[string]struct{})
	chain := make([]OwnershipEntry, 0, 4)

	for len(chain) < maxOwnershipChainLength {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := seen[current.String()]; ok {
			return nil, fmt.Errorf("cycle in ordinal ownership chain at %s", current.String())
		}
		seen[current.String()] = struct{}{}

		output, err := o.loadOutput(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("failed to load ownership output %s: %w", current.String(), err)
		}
		if output.Satoshis != 1 {
			return nil, fmt.Errorf("ownership output %s is not a 1-sat ordinal", current.String())
		}

		chain = append(chain, OwnershipEntry{
			Outpoint: *current,
			Address:  controllingP2PKHAddress(output),
		})

		spendTxid, err := o.loadSpend(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("failed to load spend for %s: %w", current.String(), err)
		}
		if spendTxid == nil {
			return chain, nil
		}

		spendTx, err := o.loadTx(ctx, spendTxid.String())
		if err != nil {
			return nil, fmt.Errorf("failed to load spending tx %s: %w", spendTxid.String(), err)
		}
		next, err := o.calculateOrdinalOutput(ctx, spendTx, current)
		if err != nil {
			return nil, fmt.Errorf("failed to follow ordinal spend from %s: %w", current.String(), err)
		}
		if next == nil {
			return chain, nil
		}
		current = next
	}

	return nil, fmt.Errorf("ordinal ownership chain exceeds %d entries", maxOwnershipChainLength)
}

func controllingP2PKHAddress(output *transaction.TransactionOutput) string {
	if output == nil || output.LockingScript == nil {
		return ""
	}

	lockingScript := output.LockingScript.Bytes()
	if len(lockingScript) >= 25 {
		prefix := script.Script(lockingScript[:25])
		if address := p2pkh.Decode(&prefix, true); address != nil {
			return address.AddressString
		}
	}

	decoded := inscription.Decode(output.LockingScript)
	if decoded == nil {
		return ""
	}
	suffix := decoded.ScriptSuffix
	if len(suffix) > 0 && suffix[0] == script.OpCODESEPARATOR {
		suffix = suffix[1:]
	}
	if len(suffix) < 25 {
		return ""
	}

	if address := p2pkh.Decode(script.NewFromBytes(suffix[:25]), true); address != nil {
		return address.AddressString
	}
	return ""
}
