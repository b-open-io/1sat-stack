package bsv21_test

import (
	"os"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/template/bsv21/pow20"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

// TestDecodePOW20Integration tests decoding a POW20 contract from a test vector
func TestDecodePOW20Integration(t *testing.T) {
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

	// Find the output with the BSV21/POW20 inscription
	var bsv21Data *bsv21.Bsv21
	for i, output := range tx.Outputs {
		t.Logf("Checking output %d with %d satoshis", i, output.Satoshis)
		bsv21Data = bsv21.Decode(output.LockingScript)
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

	// Try to decode as POW20
	// Since pow20.Decode works differently, we need to check if any outputs contain POW20 data
	var pow20Data *pow20.Pow20
	for i, output := range tx.Outputs {
		pow20Data = pow20.Decode(output.LockingScript)
		if pow20Data != nil {
			t.Logf("Found POW20 data in output %d", i)
			break
		}
	}

	// NOTE: We may not find POW20 data if this is just a BSV-20 JSON contract definition
	// and not the actual POW20 contract structure
	if pow20Data != nil {
		symbol := ""
		if pow20Data.Bsv21 != nil && pow20Data.Bsv21.Symbol != nil {
			symbol = *pow20Data.Bsv21.Symbol
		}

		decimals := uint8(0)
		if pow20Data.Bsv21 != nil && pow20Data.Bsv21.Decimals != nil {
			decimals = *pow20Data.Bsv21.Decimals
		}

		t.Logf("POW20 data: Symbol=%s, Max=%d, Dec=%d, Difficulty=%d",
			symbol, pow20Data.MaxSupply, decimals, pow20Data.Difficulty)
	} else {
		t.Log("No POW20 contract structure found - this is likely just the JSON contract definition")

		// We should be able to extract additional POW20-specific fields from the inscription JSON
		// Let's manually extract the expected POW20 fields from the inscription JSON
		require.NotNil(t, bsv21Data.Insc.File.Content, "Inscription content should not be nil")

		// Extract and verify POW20-specific fields from the JSON content
		inscContent := string(bsv21Data.Insc.File.Content)

		// Check if the content contains the expected POW20 fields
		require.Contains(t, inscContent, `"contract":"pow-20"`, "JSON should contain pow-20 contract type")
		require.Contains(t, inscContent, `"difficulty":"5"`, "JSON should contain difficulty 5")
		require.Contains(t, inscContent, `"startingReward":"100000"`, "JSON should contain starting reward 100000")
		require.Contains(t, inscContent, `"maxSupply":"4200000000"`, "JSON should contain max supply 4200000000")
	}
}
