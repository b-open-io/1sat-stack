package bsv21

import (
	"os"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

// TestDecodeBSV21 tests decoding a BSV21 token from a test vector
func TestDecodeBSV21(t *testing.T) {
	// Load the test vector hex data
	hexData, err := os.ReadFile("testdata/dfa24771dbd093efbddf19ec424eab60113e288672c23182be75ec3f5452ba8d.hex")
	require.NoError(t, err, "Failed to read test vector hex data")

	// Create a transaction from the hex data
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexData)))
	require.NoError(t, err, "Failed to create transaction from hex data")

	// Verify the transaction ID
	expectedTxID := "dfa24771dbd093efbddf19ec424eab60113e288672c23182be75ec3f5452ba8d"
	require.Equal(t, expectedTxID, tx.TxID().String(), "Transaction ID should match the expected value")

	// Log transaction info
	t.Logf("Transaction ID: %s", tx.TxID().String())
	t.Logf("Transaction has %d inputs and %d outputs", len(tx.Inputs), len(tx.Outputs))

	// Find the output with the BSV21 inscription
	var bsv21Data *Bsv21
	for i, output := range tx.Outputs {
		t.Logf("Checking output %d with %d satoshis", i, output.Satoshis)
		bsv21Data = Decode(output.LockingScript)
		if bsv21Data != nil {
			t.Logf("Found BSV21 data in output %d", i)
			break
		}
	}

	// Make sure we found the BSV21 data
	require.NotNil(t, bsv21Data, "Should find BSV21 data in one of the outputs")

	// Verify BSV21 data
	t.Logf("BSV21 data: Op=%s, Amt=%d", bsv21Data.Op, bsv21Data.Amt)
	require.Equal(t, "deploy+mint", bsv21Data.Op, "Operation should be deploy+mint")
	require.Equal(t, uint64(4200000000), bsv21Data.Amt, "Amount should be 4200000000")

	// Check the Symbol (sym)
	require.NotNil(t, bsv21Data.Symbol, "Symbol should not be nil")
	require.Equal(t, "BUIDL", *bsv21Data.Symbol, "Symbol should be BUIDL")

	// Check the Decimals (dec)
	require.NotNil(t, bsv21Data.Decimals, "Decimals should not be nil")
	require.Equal(t, uint8(2), *bsv21Data.Decimals, "Decimals should be 2")

	// Check the Icon
	require.NotNil(t, bsv21Data.Icon, "Icon should not be nil")
	require.Equal(t, "df3ceacd1a4169ec7cca3037ca2714f5fcdc0bbdb88ebfd3609257faa4814809_0", *bsv21Data.Icon, "Icon should match expected value")

	// Verify the inscription file data
	require.NotNil(t, bsv21Data.Insc, "Inscription should not be nil")
	require.Equal(t, "application/bsv-20", bsv21Data.Insc.File.Type, "File type should be application/bsv-20")
}
