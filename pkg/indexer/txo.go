package indexer

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OutpointToTypes converts a transaction.Outpoint to types.Outpoint
func OutpointToTypes(op *transaction.Outpoint) *types.Outpoint {
	if op == nil {
		return nil
	}
	return types.NewOutpoint(*op)
}
