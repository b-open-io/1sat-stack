package parse

import (
	"strconv"
	"strings"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// NormalizeRelativeOutpoint expands same-transaction relative outpoints of the
// form "_N" to "{txid}_N" using the given output's transaction. Absolute
// outpoints and other values are returned unchanged.
func NormalizeRelativeOutpoint(ref string, outpoint *transaction.Outpoint) string {
	if outpoint == nil || !strings.HasPrefix(ref, "_") {
		return ref
	}
	index, err := strconv.ParseUint(strings.TrimPrefix(ref, "_"), 10, 32)
	if err != nil {
		return ref
	}
	return (&transaction.Outpoint{
		Txid:  outpoint.Txid,
		Index: uint32(index),
	}).OrdinalString()
}
