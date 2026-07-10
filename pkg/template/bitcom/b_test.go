package bitcom

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

// TestVector represents a single test case
type TestVector struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	RawTransaction string         `json:"raw_transaction,omitempty"`
	Expected       map[string]any `json:"expected"`
}

// TestVectors represents a collection of test cases
type TestVectors struct {
	Description string       `json:"description"`
	Version     string       `json:"version"`
	Vectors     []TestVector `json:"vectors"`
}

// loadTestVectors loads and parses test vectors from a JSON file
func loadTestVectors(t *testing.T, filePath string) TestVectors {
	t.Helper()

	// Read test vectors file
	data, err := os.ReadFile(filePath)
	require.NoError(t, err, "Failed to read test vectors file: %s", filePath)

	// Parse test vectors
	var vectors TestVectors
	err = json.Unmarshal(data, &vectors)
	require.NoError(t, err, "Failed to parse test vectors")

	return vectors
}

// getTransactionFromVector loads a transaction from a file based on the txid in the test vector
func getTransactionFromVector(t *testing.T, vector TestVector) *transaction.Transaction {
	t.Helper()

	// Get transaction ID from expected values
	var txID string
	if id, ok := vector.Expected["tx_id"].(string); ok && id != "" {
		txID = id
	} else {
		t.Logf("No tx_id found in expected values for test vector '%s'", vector.Name)
		return nil
	}

	// Construct the file path from the txID
	filePath := "testdata/" + txID + ".hex"
	t.Logf("Attempting to read transaction from file: %s", filePath)

	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Logf("Failed to read transaction file '%s': %v", filePath, err)
		return nil
	}

	// Clean up the hex data
	rawTx := strings.TrimSpace(string(data))
	t.Logf("Read transaction hex from file, length: %d characters", len(rawTx))

	// Skip if empty
	if rawTx == "" {
		t.Skipf("Skipping test vector '%s' because raw transaction is empty", vector.Name)
		return nil
	}

	// Parse raw transaction
	tx, err := transaction.NewTransactionFromHex(rawTx)
	if err != nil {
		t.Errorf("Failed to parse raw transaction for test vector '%s': %v", vector.Name, err)
		return nil
	}

	return tx
}

// TestDecodeB tests the DecodeB function against real-world transaction data
func TestDecodeB(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test with nil script
	result := DecodeB(nil)
	require.Nil(t, result, "Expected nil result for nil script")

	// Load test vectors
	vectors := loadTestVectors(t, "testdata/post_test_vectors.json")

	// Test with real transaction data
	for _, vector := range vectors.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			// Reset global state before each subtest
			resetTestState()

			// Get transaction
			tx := getTransactionFromVector(t, vector)
			require.NotNil(t, tx, "Expected valid transaction for test vector")

			// Check each output for B protocol data
			for _, output := range tx.Outputs {
				if output.LockingScript == nil {
					continue
				}

				// First find the OP_RETURN
				pos := findReturn(output.LockingScript, 0)
				if pos == -1 {
					continue
				}

				// Then check for B protocol data
				bc := Decode(output.LockingScript)
				if bc == nil {
					continue
				}

				// Look for B protocol
				for _, proto := range bc.Protocols {
					if proto.Protocol == BPrefix {
						b := DecodeB(proto.Script)
						require.NotNil(t, b, "Expected valid B protocol data")
						require.NotEmpty(t, b.Data, "Expected non-empty B protocol data")
					}
				}
			}
		})
	}
}

// TestDecodeB_Bytes tests that DecodeB correctly handles raw bytes input
func TestDecodeB_Bytes(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test nil input
	result := DecodeB(nil)
	require.Nil(t, result, "Expected nil result for nil input")

	// Create a script with just the B protocol content (data, mediaType, encoding)
	s := &script.Script{}
	_ = s.AppendPushData([]byte("Hello World"))
	_ = s.AppendPushData([]byte(string(MediaTypeTextPlain)))
	_ = s.AppendPushData([]byte(string(EncodingUTF8)))

	// Create a copy to read from
	scriptCopy := *s

	// Test decoding directly from script
	result = DecodeB(s)
	require.NotNil(t, result, "Expected non-nil result for valid B protocol script")
	require.Equal(t, "Hello World", string(result.Data), "Expected correct data")
	require.Equal(t, MediaTypeTextPlain, result.MediaType, "Expected correct media type")
	require.Equal(t, EncodingUTF8, result.Encoding, "Expected correct encoding")

	// Reset global state before testing with bytes
	resetTestState()

	// Use ToScript to convert bytes to script
	scriptBytes := scriptCopy.Bytes()
	scriptFromBytes := ToScript(scriptBytes)
	require.NotNil(t, scriptFromBytes, "Expected valid script from bytes")

	// Test with script created from bytes
	result = DecodeB(scriptFromBytes)
	require.NotNil(t, result, "Expected non-nil result for valid B protocol script bytes")
	require.Equal(t, "Hello World", string(result.Data), "Expected correct data")
	require.Equal(t, MediaTypeTextPlain, result.MediaType, "Expected correct media type")
	require.Equal(t, EncodingUTF8, result.Encoding, "Expected correct encoding")

	// Test invalid script bytes (not enough chunks)
	invalidBytes := []byte{0x00, 0x01} // Just random bytes that are not a valid script
	result = DecodeB(invalidBytes)
	require.Nil(t, result, "Expected nil result for invalid script bytes")
}
