package bsv21

import (
	"fmt"
	"math"
	"testing"

	bsv21template "github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/stretchr/testify/require"
)

const overflowTestTokenID = "dfa24771dbd093efbddf19ec424eab60113e288672c23182be75ec3f5452ba8d_0"

func transferScript(t *testing.T, suffix *script.Script, amt uint64) *script.Script {
	t.Helper()
	lock, err := (&inscription.Inscription{
		File: inscription.File{
			Type: "application/bsv-20",
			Content: []byte(fmt.Sprintf(
				`{"p":"bsv-20","op":"transfer","id":"%s","amt":"%d"}`,
				overflowTestTokenID, amt,
			)),
		},
		ScriptSuffix: *suffix,
	}).Lock()
	require.NoError(t, err)
	return lock
}

// spendOneToken builds a spend of a single 1-token UTXO into transfer outputs
// with the given amounts and returns the admitted output indices.
func spendOneToken(t *testing.T, outAmts ...uint64) []uint32 {
	t.Helper()
	priv, err := ec.NewPrivateKey()
	require.NoError(t, err)
	addr, err := script.NewAddressFromPublicKey(priv.PubKey(), true)
	require.NoError(t, err)
	p2pkhLock, err := p2pkh.Lock(addr)
	require.NoError(t, err)
	unlocker, err := p2pkh.Unlock(priv, nil)
	require.NoError(t, err)

	source := transaction.NewTransaction()
	source.AddOutput(&transaction.TransactionOutput{
		Satoshis: 1_000, LockingScript: transferScript(t, p2pkhLock, 1),
	})

	spend := transaction.NewTransaction()
	spend.AddInputFromTx(source, 0, unlocker)
	for _, amt := range outAmts {
		spend.AddOutput(&transaction.TransactionOutput{
			Satoshis: 1, LockingScript: transferScript(t, p2pkhLock, amt),
		})
	}
	require.NoError(t, spend.Sign())

	beef := transaction.NewBeef()
	_, err = beef.MergeTransaction(spend)
	require.NoError(t, err)

	tm := NewBsv21ValidatedTopicManager("tm_bsv21", nil, nil, nil)
	admit, err := tm.IdentifyAdmissibleOutputs(t.Context(), beef, spend.TxID(), []uint32{0})
	require.NoError(t, err)
	return admit.OutputsToAdmit
}

// Regression: a 1-token input split into outputs of MaxUint64 and 2 wraps to
// a sum of 1 under unchecked uint64 addition and used to be admitted.
func TestTransferOutputSumOverflowNotAdmitted(t *testing.T) {
	require.Equal(t, uint64(math.MaxUint64), bsv21template.Decode(
		transferScript(t, &script.Script{}, math.MaxUint64)).Amt)
	require.Empty(t, spendOneToken(t, math.MaxUint64, 2))
	require.Empty(t, spendOneToken(t, math.MaxUint64, 1))
	require.Empty(t, spendOneToken(t, math.MaxUint64, math.MaxUint64))
}

// A legitimate 1-token spend is still admitted.
func TestTransferBalancedSpendAdmitted(t *testing.T) {
	require.ElementsMatch(t, []uint32{0}, spendOneToken(t, 1))
	require.Empty(t, spendOneToken(t, 2))
}
