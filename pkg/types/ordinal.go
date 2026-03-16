package types

import (
	"fmt"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OrdinalOutput finds which output receives the ordinal from a given input.
// Returns nil if the ordinal is not transferred (e.g. absorbed into a multi-sat output).
// Returns an error only if the transaction is missing required source outputs.
func OrdinalOutput(tx *transaction.Transaction, vin uint32) (*uint32, error) {
	if int(vin) >= len(tx.Inputs) {
		return nil, fmt.Errorf("input index %d out of range", vin)
	}

	var ordinalOffset uint64
	for i := uint32(0); i < vin; i++ {
		src := tx.Inputs[i].SourceTxOutput()
		if src == nil {
			return nil, fmt.Errorf("missing source output for input %d", i)
		}
		ordinalOffset += src.Satoshis
	}

	src := tx.Inputs[vin].SourceTxOutput()
	if src == nil {
		return nil, fmt.Errorf("missing source output for input %d", vin)
	}
	if src.Satoshis != 1 {
		return nil, nil
	}

	var cumulative uint64
	for vout, output := range tx.Outputs {
		if output.Satoshis == 0 {
			continue
		}
		if cumulative == ordinalOffset {
			if output.Satoshis != 1 {
				return nil, nil
			}
			v := uint32(vout)
			return &v, nil
		}
		cumulative += output.Satoshis
		if cumulative > ordinalOffset {
			return nil, nil
		}
	}

	return nil, nil
}

// OrdinalInput finds which input provided the ordinal for a given 1-sat output.
// Returns nil if the output is a fresh inscription (no 1-sat ordinal on the input side).
// Returns an error only if the transaction is missing required source outputs.
func OrdinalInput(tx *transaction.Transaction, vout uint32) (*uint32, error) {
	if int(vout) >= len(tx.Outputs) {
		return nil, fmt.Errorf("output index %d out of range", vout)
	}

	if tx.Outputs[vout].Satoshis != 1 {
		return nil, nil
	}

	var ordinalOffset uint64
	for i := uint32(0); i < vout; i++ {
		ordinalOffset += tx.Outputs[i].Satoshis
	}

	var cumulative uint64
	for vin, input := range tx.Inputs {
		src := input.SourceTxOutput()
		if src == nil {
			return nil, fmt.Errorf("missing source output for input %d", vin)
		}
		if cumulative == ordinalOffset {
			if src.Satoshis != 1 {
				return nil, nil
			}
			v := uint32(vin)
			return &v, nil
		}
		cumulative += src.Satoshis
		if cumulative > ordinalOffset {
			return nil, nil
		}
	}

	return nil, nil
}
