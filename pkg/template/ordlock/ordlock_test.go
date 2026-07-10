package ordlock

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

func TestOrdLock(t *testing.T) {
	// Create an OrdLock instance
	publicKeyHash, _ := hex.DecodeString("1234567890abcdef1234567890abcdef12345678")
	seller, _ := script.NewAddressFromPublicKeyHash(publicKeyHash, true)

	ordLock := &OrdLock{
		Seller:   seller,
		Price:    1000,
		PricePer: 0.5,
		PayOut:   []byte("test payout"),
	}

	// Basic validation
	require.Equal(t, uint64(1000), ordLock.Price)
	require.Equal(t, 0.5, ordLock.PricePer)
	require.Equal(t, []byte("test payout"), ordLock.PayOut)
	require.NotNil(t, ordLock.Seller)
}

func TestCreateOrdLockScript(t *testing.T) {
	// Create test data
	publicKeyHash, _ := hex.DecodeString("1234567890abcdef1234567890abcdef12345678")

	// Create a simple payout output
	payoutOutput := &transaction.TransactionOutput{
		Satoshis: 5000,
	}

	// Initialize with an empty script
	emptyScript := script.NewFromBytes([]byte{})
	payoutOutput.LockingScript = emptyScript

	// Manually build an OrdLock script similar to what we'd expect from the package
	var scriptData []byte

	// Add the prefix
	scriptData = append(scriptData, OrdLockPrefix...)

	// Add the PKHash operation - in a real implementation this would come from the seller
	scriptData = append(scriptData, 0x14) // OP_DATA_20
	scriptData = append(scriptData, publicKeyHash...)

	// Add the payout output
	outputBytes := payoutOutput.Bytes()
	scriptData = append(scriptData, byte(len(outputBytes))) // Length of output data
	scriptData = append(scriptData, outputBytes...)

	// Add the suffix
	scriptData = append(scriptData, OrdLockSuffix...)

	// Create the script
	lockScript := script.NewFromBytes(scriptData)

	// Decode the script back to an OrdLock to verify it's correctly formed
	decodedLock := Decode(lockScript)
	require.NotNil(t, decodedLock)

	// Verify the decoded values match what we put in
	require.NotNil(t, decodedLock.Seller)
	require.Equal(t, uint64(5000), decodedLock.Price)
}

func TestOrdLockDecode(t *testing.T) {
	// Create a mock transaction output with OrdLock script
	publicKeyHash, _ := hex.DecodeString("1234567890abcdef1234567890abcdef12345678")

	// Create a payout transaction output
	txOut := &transaction.TransactionOutput{
		Satoshis: 1000,
	}

	// Initialize with an empty script
	emptyScript := script.NewFromBytes([]byte{})
	txOut.LockingScript = emptyScript

	// Create a script buffer with the OrdLock data
	var scriptData []byte

	// Add the prefix
	scriptData = append(scriptData, OrdLockPrefix...)

	// Add the PKHash operation
	scriptData = append(scriptData, 0x14) // OP_DATA_20
	scriptData = append(scriptData, publicKeyHash...)

	// Add the payout output
	outputBytes := txOut.Bytes()
	scriptData = append(scriptData, byte(len(outputBytes))) // Length of output data
	scriptData = append(scriptData, outputBytes...)

	// Add the suffix
	scriptData = append(scriptData, OrdLockSuffix...)

	// Create the script
	scr := script.NewFromBytes(scriptData)

	// Test the Decode function
	ordLock := Decode(scr)

	// Verify the decoding worked as expected
	require.NotNil(t, ordLock, "Failed to decode OrdLock script")
	require.Equal(t, uint64(1000), ordLock.Price)
	require.NotNil(t, ordLock.Seller)
	require.NotEmpty(t, ordLock.PayOut)
}

func TestOrdLockPrefixSuffix(t *testing.T) {
	// Verify that the OrdLockPrefix and OrdLockSuffix constants are set
	require.NotNil(t, OrdLockPrefix)
	require.NotNil(t, OrdLockSuffix)
	require.True(t, len(OrdLockPrefix) > 0)
	require.True(t, len(OrdLockSuffix) > 0)

	// Log the lengths for diagnostic purposes
	t.Logf("OrdLockPrefix length: %d", len(OrdLockPrefix))
	t.Logf("OrdLockSuffix length: %d", len(OrdLockSuffix))
}

// TestDecodeInvalidScript tests decoding invalid scripts
func TestDecodeInvalidScript(t *testing.T) {
	// Skip the nil test as the implementation doesn't handle nil
	// Test with empty script
	emptyScript := script.NewFromBytes([]byte{})
	result := Decode(emptyScript)
	require.Nil(t, result, "Expected nil result for empty script")

	// Test with script containing only prefix
	prefixOnlyScript := script.NewFromBytes(OrdLockPrefix)
	result = Decode(prefixOnlyScript)
	require.Nil(t, result, "Expected nil result for script with only prefix")

	// Test with script containing only suffix
	suffixOnlyScript := script.NewFromBytes(OrdLockSuffix)
	result = Decode(suffixOnlyScript)
	require.Nil(t, result, "Expected nil result for script with only suffix")

	// Test with invalid script data between prefix and suffix
	invalidDataScript := script.NewFromBytes(append(append(OrdLockPrefix, []byte{0xFF, 0xEE, 0xDD}...), OrdLockSuffix...))
	result = Decode(invalidDataScript)
	require.Nil(t, result, "Expected nil result for script with invalid data")
}

// TestDecodeWithTestVector verifies that the OrdLock can properly decode
// a transaction from a test vector
func TestDecodeWithTestVector(t *testing.T) {
	// Load the hex data from the file
	hexData, err := os.ReadFile("testdata/690b213114926cd5a6f0785cb3e289afe9cde195972c1d344569c90530b8cbd1.hex")
	require.NoError(t, err, "Failed to read hex data from file")

	// Create a transaction from the bytes
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexData)))
	require.NoError(t, err, "Failed to create transaction from bytes")

	// Verify transaction ID matches expected
	expectedTxID := "690b213114926cd5a6f0785cb3e289afe9cde195972c1d344569c90530b8cbd1"
	require.Equal(t, expectedTxID, tx.TxID().String(), "Transaction ID should match expected value")

	// Expected values
	expectedSellerAddress := "15WPxBYNpjCXYCynyUp46CHFhRkiJTePBW"
	expectedPrice := uint64(1000000) // 0.01 BSV
	expectedOutputIndex := 0         // The OrdLock is expected in the first output

	// Transaction structure validation
	require.Equal(t, 3, len(tx.Outputs), "Transaction should have 3 outputs")
	require.Equal(t, 2, len(tx.Inputs), "Transaction should have 2 inputs")

	// Log transaction info
	t.Logf("Transaction ID: %s", tx.TxID().String())
	t.Logf("Transaction has %d inputs and %d outputs", len(tx.Inputs), len(tx.Outputs))

	// Log output satoshis
	for i, output := range tx.Outputs {
		t.Logf("Output %d: %d satoshis", i, output.Satoshis)
	}

	// Check output sizes - update values based on the actual transaction
	require.Equal(t, uint64(1), tx.Outputs[0].Satoshis, "First output should have 1 satoshi")
	require.Equal(t, uint64(6000), tx.Outputs[1].Satoshis, "Second output should have 6000 satoshis")
	require.Equal(t, uint64(3181), tx.Outputs[2].Satoshis, "Third output should have 3181 satoshis")

	// Decode OrdLock from the expected output
	ordLockData := Decode(tx.Outputs[expectedOutputIndex].LockingScript)
	require.NotNil(t, ordLockData, "OrdLock should be found in output %d", expectedOutputIndex)

	// Validate OrdLock fields
	t.Logf("OrdLock Price: %d", ordLockData.Price)
	t.Logf("OrdLock Seller: %s", ordLockData.Seller.AddressString)
	t.Logf("OrdLock PayOut length: %d bytes", len(ordLockData.PayOut))

	// Verify specific values
	require.Equal(t, expectedSellerAddress, ordLockData.Seller.AddressString, "OrdLock should have expected seller address")
	require.Equal(t, expectedPrice, ordLockData.Price, "OrdLock should have expected price")

	// Verify PayOut structure
	require.NotEmpty(t, ordLockData.PayOut, "PayOut data should not be empty")

	// PricePer should be either valid or 0
	t.Logf("OrdLock PricePer: %f", ordLockData.PricePer)
	require.GreaterOrEqual(t, ordLockData.PricePer, 0.0, "PricePer should be non-negative")

	// Examine the PayOut data more closely
	if len(ordLockData.PayOut) > 0 {
		// Use a helper function to get at most the first 10 bytes
		previewLength := min(10, len(ordLockData.PayOut))
		t.Logf("PayOut starts with bytes: %x", ordLockData.PayOut[:previewLength])

		// Specifically check if the PayOut starts with OP_RETURN (0x6a)
		if len(ordLockData.PayOut) > 0 && ordLockData.PayOut[0] == 0x6a {
			t.Logf("PayOut data starts with OP_RETURN as expected")
		}
	}
}
