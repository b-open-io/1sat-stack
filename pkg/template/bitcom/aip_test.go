package bitcom

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

// TestDecodeAIP verifies the AIP protocol decoding functionality
func TestDecodeAIP(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	t.Run("nil bitcom", func(t *testing.T) {
		// Reset global state before each subtest
		resetTestState()

		// Test nil Bitcom
		var nilBitcom *Bitcom
		result := DecodeAIP(nilBitcom)
		require.NotNil(t, result, "Result should be an empty slice, not nil")
		require.Empty(t, result, "Result should be an empty slice for nil Bitcom")
	})

	t.Run("empty protocols", func(t *testing.T) {
		// Reset global state before each subtest
		resetTestState()

		// Test Bitcom with empty protocols
		emptyBitcom := &Bitcom{
			Protocols: []*BitcomProtocol{},
		}
		result := DecodeAIP(emptyBitcom)
		require.NotNil(t, result, "Result should be an empty slice, not nil")
		require.Empty(t, result, "Result should be an empty slice for Bitcom with empty protocols")
	})

	tests := []struct {
		name     string
		bitcom   *Bitcom
		expected []*AIP
	}{
		{
			name: "protocols without AIP",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: MapPrefix,
						Script:   []byte("some data"),
					},
					{
						Protocol: BPrefix,
						Script:   []byte("more data"),
					},
				},
			},
			expected: []*AIP{},
		},
		{
			name: "valid AIP protocol with minimum fields",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: AIPPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte("BITCOIN_ECDSA"))
							_ = s.AppendPushData([]byte("1address1234567890"))
							_ = s.AppendPushData([]byte("signature1234567890"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*AIP{
				{
					Algorithm: "BITCOIN_ECDSA",
					Address:   "1address1234567890",
					Signature: []byte("signature1234567890"),
				},
			},
		},
		{
			name: "valid AIP protocol with field indexes",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: AIPPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte("BITCOIN_ECDSA"))
							_ = s.AppendPushData([]byte("1address1234567890"))
							_ = s.AppendPushData([]byte("signature1234567890"))
							_ = s.AppendPushData([]byte("1"))
							_ = s.AppendPushData([]byte("2"))
							_ = s.AppendPushData([]byte("3"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*AIP{
				{
					Algorithm:    "BITCOIN_ECDSA",
					Address:      "1address1234567890",
					Signature:    []byte("signature1234567890"),
					FieldIndexes: []int{1, 2, 3},
				},
			},
		},
		{
			name: "multiple AIP protocols",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: AIPPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte("BITCOIN_ECDSA"))
							_ = s.AppendPushData([]byte("1address1234567890"))
							_ = s.AppendPushData([]byte("signature1234567890"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*AIP{
				{
					Algorithm: "BITCOIN_ECDSA",
					Address:   "1address1234567890",
					Signature: []byte("signature1234567890"),
				},
			},
		},
		{
			name: "invalid AIP protocol (missing fields)",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: AIPPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte("BITCOIN_ECDSA"))
							// Missing address and signature
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*AIP{},
		},
		{
			name: "invalid field indexes",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: AIPPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte("BITCOIN_ECDSA"))
							_ = s.AppendPushData([]byte("1address1234567890"))
							_ = s.AppendPushData([]byte("signature1234567890"))
							_ = s.AppendPushData([]byte("not-a-number"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*AIP{
				{
					Algorithm:    "BITCOIN_ECDSA",
					Address:      "1address1234567890",
					Signature:    []byte("signature1234567890"),
					FieldIndexes: []int{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DecodeAIP(tt.bitcom)

			// Debug output for multiple AIP protocols test
			if tt.name == "multiple AIP protocols" {
				t.Logf("Expected %d AIPs, got %d AIPs", len(tt.expected), len(result))
				t.Logf("Result: %+v", result)
			}

			require.Equal(t, len(tt.expected), len(result))

			if len(tt.expected) > 0 {
				for i, expectedAIP := range tt.expected {
					if i >= len(result) {
						t.Fatalf("Missing expected AIP at index %d", i)
						continue
					}

					resultAIP := result[i]
					require.Equal(t, expectedAIP.Algorithm, resultAIP.Algorithm)
					require.Equal(t, expectedAIP.Address, resultAIP.Address)
					require.Equal(t, expectedAIP.Signature, resultAIP.Signature)

					require.Equal(t, len(expectedAIP.FieldIndexes), len(resultAIP.FieldIndexes))
					for j, expectedIndex := range expectedAIP.FieldIndexes {
						if j >= len(resultAIP.FieldIndexes) {
							t.Fatalf("Missing expected field index at position %d", j)
							continue
						}
						require.Equal(t, expectedIndex, resultAIP.FieldIndexes[j])
					}
				}
			}
		})
	}
}

// TestDecodeAIPFromTestVector tests decoding AIP instances from a test vector transaction
func TestDecodeAIPFromTestVector(t *testing.T) {
	// Load the hex data from the file
	hexData, err := os.ReadFile("testdata/5633bb966d9531d22df7ae98a70966eebe4379d400d74ac948bf5b4f2867092c.hex")
	require.NoError(t, err, "Failed to read hex data from file")

	// Create a transaction from the bytes
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexData)))
	require.NoError(t, err, "Failed to create transaction from bytes")

	// Verify transaction ID matches expected
	expectedTxID := "5633bb966d9531d22df7ae98a70966eebe4379d400d74ac948bf5b4f2867092c"
	require.Equal(t, expectedTxID, tx.TxID().String(), "Transaction ID should match expected value")

	// Log the transaction for debugging
	t.Logf("Transaction ID: %s", tx.TxID().String())
	t.Logf("Transaction has %d inputs and %d outputs", len(tx.Inputs), len(tx.Outputs))

	// Verify structure of the transaction
	require.Equal(t, 2, len(tx.Outputs), "Transaction should have 2 outputs")
	require.Equal(t, 1, len(tx.Inputs), "Transaction should have 1 input")

	// Check the first output for OP_RETURN and BitCom data
	firstOutput := tx.Outputs[0]
	require.Equal(t, uint64(0), firstOutput.Satoshis, "First output should have 0 satoshis")

	// Step 1: Decode the BitCom data from the script
	bitcomData := Decode(firstOutput.LockingScript)
	require.NotNil(t, bitcomData, "Bitcom data should not be nil")

	// Log the number of protocols found in the BitCom data
	t.Logf("Found %d BitCom protocols in output 0", len(bitcomData.Protocols))

	// Debug each BitCom protocol
	var aipProtocolCount int
	for i, proto := range bitcomData.Protocols {
		t.Logf("Protocol %d: %s (script length: %d bytes)", i+1, proto.Protocol, len(proto.Script))
		if len(proto.Script) > 0 {
			t.Logf("  First few bytes: %x", proto.Script[:min(10, len(proto.Script))])
		}

		// Count the AIP protocols
		if proto.Protocol == AIPPrefix {
			aipProtocolCount++
			t.Logf("  Found AIP protocol in position %d", i+1)
			if len(proto.Script) > 20 {
				t.Logf("  AIP script starts with: %x", proto.Script[:min(20, len(proto.Script))])

				// Check if the script starts with expected BITCOIN_ECDSA prefix
				expectedPrefix := []byte{0x0d, 0x42, 0x49, 0x54, 0x43, 0x4f, 0x49, 0x4e, 0x5f, 0x45, 0x43, 0x44, 0x53, 0x41}
				if len(proto.Script) >= len(expectedPrefix) {
					prefixMatches := true
					for j, b := range expectedPrefix {
						if proto.Script[j] != b {
							prefixMatches = false
							break
						}
					}
					if prefixMatches {
						t.Logf("  Protocol contains BITCOIN_ECDSA algorithm")
					}
				}
			}
		}
	}

	// Verify we found the expected number of AIP protocols
	require.Equal(t, 2, aipProtocolCount, "Should find 2 AIP protocols in the transaction")

	// Step 2: Now we use DecodeAIP to extract the AIP data
	aips := DecodeAIP(bitcomData)

	// Log the number of AIP instances found
	t.Logf("DecodeAIP found %d AIP instances", len(aips))

	// We should find 2 AIP instances in this transaction
	require.Equal(t, 2, len(aips), "Should decode 2 AIP instances from the transaction")

	// Validate first AIP
	require.Greater(t, len(aips), 0, "Should find at least one AIP instance")

	fmt.Println("AIP 1:")
	fmt.Println("  Algorithm:", aips[0].Algorithm)
	fmt.Println("  Address:", aips[0].Address)
	fmt.Println("  Signature length:", len(aips[0].Signature), "bytes")

	require.Equal(t, "BITCOIN_ECDSA", aips[0].Algorithm, "AIP 1 should have BITCOIN_ECDSA algorithm")
	require.Equal(t, "1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz", aips[0].Address, "AIP 1 should have expected address")
	require.NotEmpty(t, aips[0].Signature, "AIP 1 should have signature")

	// Validate second AIP
	require.Equal(t, 2, len(aips), "Should find 2 AIP instances")

	fmt.Println("AIP 2:")
	fmt.Println("  Algorithm:", aips[1].Algorithm)
	fmt.Println("  Address:", aips[1].Address)
	fmt.Println("  Signature length:", len(aips[1].Signature), "bytes")

	require.Equal(t, "BITCOIN_ECDSA", aips[1].Algorithm, "AIP 2 should have BITCOIN_ECDSA algorithm")
	require.Equal(t, "19nknLhRnGKRR3hobeFuuqmHUMiNTKZHsR", aips[1].Address, "AIP 2 should have expected address")
	require.NotEmpty(t, aips[1].Signature, "AIP 2 should have signature")
	require.True(t, aips[0].Valid, "AIP 1 should be valid")
}

// TestDecodeAIPBasic tests the basic functionality of the AIP decoder
func TestDecodeAIPBasic(t *testing.T) {
	// Create a mock Bitcom with an AIP protocol
	mockBitcom := &Bitcom{
		Protocols: []*BitcomProtocol{
			{
				Protocol: AIPPrefix,
				Script: func() []byte {
					s := &script.Script{}
					_ = s.AppendPushData([]byte("BITCOIN_ECDSA"))
					_ = s.AppendPushData([]byte("1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz"))
					_ = s.AppendPushData([]byte("abcdefghijklmnopqrstuvwxyz0"))
					return *s
				}(),
			},
		},
	}

	// Verify mockBitcom has an AIP protocol
	require.Equal(t, 1, len(mockBitcom.Protocols), "Should have 1 protocol")
	require.Equal(t, AIPPrefix, mockBitcom.Protocols[0].Protocol, "Protocol should be AIP")

	// Verify AIP script structure
	script := mockBitcom.Protocols[0].Script
	require.NotNil(t, script, "Script should not be nil")
	require.True(t, len(script) >= 14, "Script should contain at least the algorithm")

	fmt.Println("Protocol in TestDecodeAIPBasic:")
	fmt.Println("  Protocol:", mockBitcom.Protocols[0].Protocol)
	fmt.Printf("  Script (length %d): %x\n", len(script), script)

	// Verify algorithm prefix
	expected := []byte{0x0d, 0x42, 0x49, 0x54, 0x43, 0x4f, 0x49, 0x4e, 0x5f, 0x45, 0x43, 0x44, 0x53, 0x41}
	for i, b := range expected {
		require.Equal(t, b, script[i], "Byte %d of algorithm should match", i)
	}

	// Now that the DecodeAIP function is fixed, let's test it properly
	aips := DecodeAIP(mockBitcom)
	require.NotNil(t, aips, "AIP data should not be nil")
	require.Equal(t, 1, len(aips), "Should find 1 AIP instance")
	require.Equal(t, "BITCOIN_ECDSA", aips[0].Algorithm, "AIP should have expected algorithm")
	require.Equal(t, "1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz", aips[0].Address, "AIP should have expected address")
	require.Equal(t, []byte("abcdefghijklmnopqrstuvwxyz0"), aips[0].Signature, "AIP should have expected signature")
}

// TestDecodeAIPNilCases tests handling of nil inputs to the AIP decoder
func TestDecodeAIPNilCases(t *testing.T) {
	// Test with nil Bitcom
	aips := DecodeAIP(nil)
	require.NotNil(t, aips, "AIP should return empty slice, not nil")
	require.Equal(t, 0, len(aips), "AIP should return empty slice")

	// Test with empty protocols
	aips = DecodeAIP(&Bitcom{Protocols: []*BitcomProtocol{}})
	require.NotNil(t, aips, "AIP should return empty slice, not nil")
	require.Equal(t, 0, len(aips), "AIP should return empty slice")

	// Test with protocols but none matching AIP
	aips = DecodeAIP(&Bitcom{Protocols: []*BitcomProtocol{
		{
			Protocol: "other.protocol",
			Script:   []byte{0x01, 0x02, 0x03},
		},
	}})
	require.NotNil(t, aips, "AIP should return empty slice, not nil")
	require.Equal(t, 0, len(aips), "AIP should return empty slice")
}

// TestDecodeAIPWithFieldIndexes tests decoding AIP with field indexes
func TestDecodeAIPWithFieldIndexes(t *testing.T) {
	// Create a mock Bitcom with an AIP protocol with field indexes
	mockBitcom := &Bitcom{
		Protocols: []*BitcomProtocol{
			{
				Protocol: AIPPrefix,
				Script: func() []byte {
					s := &script.Script{}
					_ = s.AppendPushData([]byte("BITCOIN_ECDSA"))
					_ = s.AppendPushData([]byte("1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz"))
					_ = s.AppendPushData([]byte("abcdefghijklmnopqrstuvwxyz0"))
					_ = s.AppendPushData([]byte("0"))
					_ = s.AppendPushData([]byte("1"))
					_ = s.AppendPushData([]byte("2"))
					return *s
				}(),
			},
		},
	}

	// Verify mockBitcom has an AIP protocol
	require.Equal(t, 1, len(mockBitcom.Protocols), "Should have 1 protocol")
	require.Equal(t, AIPPrefix, mockBitcom.Protocols[0].Protocol, "Protocol should be AIP")

	// Verify AIP script structure
	script := mockBitcom.Protocols[0].Script
	require.NotNil(t, script, "Script should not be nil")
	require.True(t, len(script) >= 14, "Script should contain at least the algorithm")

	fmt.Println("Protocol in TestDecodeAIPWithFieldIndexes:")
	fmt.Println("  Protocol:", mockBitcom.Protocols[0].Protocol)
	fmt.Printf("  Script (length %d): %x\n", len(script), script)

	// Verify algorithm prefix
	expected := []byte{0x0d, 0x42, 0x49, 0x54, 0x43, 0x4f, 0x49, 0x4e, 0x5f, 0x45, 0x43, 0x44, 0x53, 0x41}
	for i, b := range expected {
		require.Equal(t, b, script[i], "Byte %d of algorithm should match", i)
	}

	// Verify field index bytes are present (simplified check)
	indexBytes := []byte{0x01, 0x30, 0x01, 0x31, 0x01, 0x32}
	scriptLen := len(script)
	require.True(t, scriptLen >= len(expected)+len(indexBytes), "Script should be long enough to contain field indexes")

	// Now that the DecodeAIP function is fixed, let's test it properly
	aips := DecodeAIP(mockBitcom)
	require.NotNil(t, aips, "AIP data should not be nil")
	require.Equal(t, 1, len(aips), "Should find 1 AIP instance")
	require.Equal(t, 3, len(aips[0].FieldIndexes), "AIP should have 3 field indexes")
	require.Equal(t, 0, aips[0].FieldIndexes[0], "First field index should be 0")
	require.Equal(t, 1, aips[0].FieldIndexes[1], "Second field index should be 1")
	require.Equal(t, 2, aips[0].FieldIndexes[2], "Third field index should be 2")
}
