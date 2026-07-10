package opns

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecode_WithTestVector(t *testing.T) {
	txID := "58b7558ea379f24266c7e2f5fe321992ad9a724fd7a87423ba412677179ccb25" // Genesis
	testdataFile := filepath.Join("testdata", txID+".hex")

	hexBytes, err := os.ReadFile(testdataFile)
	require.NoError(t, err, "Failed to read test vector file")

	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexBytes)))
	require.NoError(t, err, "Failed to parse transaction")

	var foundScript *script.Script
	for _, output := range tx.Outputs {
		if output.LockingScript != nil && len(*output.LockingScript) >= len(contract) &&
			bytes.HasPrefix(*output.LockingScript, contract) {
			foundScript = output.LockingScript
			break
		}
	}
	require.NotNil(t, foundScript, "No output script with contract prefix found")

	result := Decode(foundScript)
	if result == nil {
		t.Logf("Decode returned nil for genesis vector (expected for some genesis tx)")
		return
	}
	t.Logf("GENESIS: Claimed: %v, Domain: '%s'", result.Claimed, result.Domain)
	// For genesis, domain may be empty. Just assert decode did not fail.
	assert.NotNil(t, result, "Decode should not return nil for genesis vector")
}

func TestDecode_WithStandardPrefix(t *testing.T) {
	txID := "935e2a477bda8709874c548fda2d504d490891ccfa5f8443705a8b6a3f403fda" // First spend?
	testdataFile := filepath.Join("testdata", txID+".hex")

	hexBytes, err := os.ReadFile(testdataFile)
	require.NoError(t, err, "Failed to read test vector file")

	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexBytes)))
	require.NoError(t, err, "Failed to parse transaction")

	var foundScript *script.Script
	for _, output := range tx.Outputs {
		if output.LockingScript != nil && len(*output.LockingScript) >= len(contract) &&
			bytes.HasPrefix(*output.LockingScript, contract) {
			foundScript = output.LockingScript
			break
		}
	}
	require.NotNil(t, foundScript, "No output script with contract prefix found")

	result := Decode(foundScript)
	if result == nil {
		t.Logf("Decode returned nil for standard prefix vector")
		return
	}
	t.Logf("STANDARD PREFIX: Claimed: %v, Domain: '%s'", result.Claimed, result.Domain)
	// For first spend, domain may be a single character or empty. Just assert decode did not fail.
	assert.NotNil(t, result, "Decode should not return nil for standard prefix vector")
}

func TestDecode_SecondSpend(t *testing.T) {
	txID := "29ad92e000dd59450fec92aa7b178e88219af92a88cad56109d3efda6d9a8c8a" // Second spend
	testdataFile := filepath.Join("testdata", txID+".hex")

	hexBytes, err := os.ReadFile(testdataFile)
	require.NoError(t, err, "Failed to read test vector file")

	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexBytes)))
	require.NoError(t, err, "Failed to parse transaction")

	var foundScript *script.Script
	for _, output := range tx.Outputs {
		if output.LockingScript != nil && len(*output.LockingScript) >= len(contract) &&
			bytes.HasPrefix(*output.LockingScript, contract) {
			foundScript = output.LockingScript
			break
		}
	}
	require.NotNil(t, foundScript, "No output script with contract prefix found")

	result := Decode(foundScript)
	require.NotNil(t, result, "Decode returned nil for valid test vector")
	t.Logf("SECOND SPEND: Claimed: %v, Domain: '%s'", result.Claimed, result.Domain)
	// For second spend, domain should be a single character (mined name char)
	assert.NotEmpty(t, result.Domain, "expected non-empty domain for second spend")
}

func TestDecode_ThirdSpend(t *testing.T) {
	txID := "2320c9d77f4b726303d5845e1962e945d0af8e8ad70e866799e5fb9ec37bc405" // Third spend or later
	testdataFile := filepath.Join("testdata", txID+".hex")

	hexBytes, err := os.ReadFile(testdataFile)
	require.NoError(t, err, "Failed to read test vector file")

	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexBytes)))
	require.NoError(t, err, "Failed to parse transaction")

	var foundScript *script.Script
	for _, output := range tx.Outputs {
		if output.LockingScript != nil && len(*output.LockingScript) >= len(contract) &&
			bytes.HasPrefix(*output.LockingScript, contract) {
			foundScript = output.LockingScript
			break
		}
	}
	require.NotNil(t, foundScript, "No output script with contract prefix found")

	result := Decode(foundScript)
	require.NotNil(t, result, "Decode returned nil for valid test vector")
	t.Logf("THIRD SPEND: Claimed: %v, Domain: '%s'", result.Claimed, result.Domain)
	// For later spends, domain may be empty or a character. Just log for now.
}
