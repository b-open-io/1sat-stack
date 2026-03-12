package overlay

import (
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
)

// MissingInputError is returned by topic managers when an input appears to belong
// to the overlay but hasn't been indexed yet. The sync worker retries the transaction
// later, giving the missing input time to be processed first.
type MissingInputError struct {
	TransactionID *chainhash.Hash
	InputIndex    uint32
	MissingTxID   *chainhash.Hash
	OutputIndex   uint32
	Topic         string
}

func (e *MissingInputError) Error() string {
	return fmt.Sprintf("missing input for %s: tx %s input[%d] references %s:%d",
		e.Topic, e.TransactionID, e.InputIndex, e.MissingTxID, e.OutputIndex)
}
