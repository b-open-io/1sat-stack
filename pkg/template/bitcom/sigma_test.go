package bitcom

import (
	"os"
	"strings"
	"testing"

	"encoding/base64"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecode verifies the Sigma protocol decoding functionality
func TestDecodeSIGMA(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	t.Run("nil bitcom", func(t *testing.T) {
		// Reset global state before each subtest
		resetTestState()

		// Test nil Bitcom
		var nilBitcom *Bitcom
		result := DecodeSIGMA(nilBitcom)
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
		result := DecodeSIGMA(emptyBitcom)
		require.NotNil(t, result, "Result should be an empty slice, not nil")
		require.Empty(t, result, "Result should be an empty slice for Bitcom with empty protocols")
	})

	tests := []struct {
		name     string
		bitcom   *Bitcom
		expected []*Sigma
	}{
		{
			name: "no sigma protocols",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: "1PuQa7K62MiKCtssSLKy1kh56WWU7MtUR5",
						Script:   []byte("some data"),
						Pos:      0,
					},
					{
						Protocol: "19HxigV4QyBv3tHpQVcUEQyq1pzZVdoAut",
						Script:   []byte("more data"),
						Pos:      0,
					},
				},
			},
			expected: []*Sigma{},
		},
		{
			name: "valid Sigma protocol with minimum fields",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: SIGMAPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte(string(AlgoECDSA)))
							_ = s.AppendPushData([]byte("1AddressBTC12345678"))
							_ = s.AppendPushData([]byte("signature1234567890"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*Sigma{
				{
					Algorithm:      AlgoECDSA,
					SignerAddress:  "1AddressBTC12345678",
					SignatureValue: base64.StdEncoding.EncodeToString([]byte("signature1234567890")),
					Valid:          true, // We now trust transaction signatures without message
				},
			},
		},
		{
			name: "valid Sigma protocol with all fields",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: SIGMAPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte(string(AlgoSHA256ECDSA)))
							_ = s.AppendPushData([]byte("1AddressBTC12345678"))
							_ = s.AppendPushData([]byte("abcdef1234567890"))
							_ = s.AppendPushData([]byte("Hello, world!")) // This is now the message field directly
							_ = s.AppendPushData([]byte("random-nonce-123"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*Sigma{
				{
					Algorithm:      AlgoSHA256ECDSA,
					SignerAddress:  "1AddressBTC12345678",
					SignatureValue: base64.StdEncoding.EncodeToString([]byte("abcdef1234567890")),
					Message:        "Hello, world!",
					Nonce:          "random-nonce-123",
				},
			},
		},
		{
			name: "multiple Sigma protocols",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: SIGMAPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte(string(AlgoECDSA)))
							_ = s.AppendPushData([]byte("1Address1"))
							_ = s.AppendPushData([]byte("signature1"))
							return *s
						}(),
						Pos: 0,
					},
					{
						Protocol: SIGMAPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte(string(AlgoSHA256ECDSA)))
							_ = s.AppendPushData([]byte("1Address2"))
							_ = s.AppendPushData([]byte("signature2"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*Sigma{
				{
					Algorithm:      AlgoECDSA,
					SignerAddress:  "1Address1",
					SignatureValue: base64.StdEncoding.EncodeToString([]byte("signature1")),
					Valid:          true, // We now trust transaction signatures without message
				},
				{
					Algorithm:      AlgoSHA256ECDSA,
					SignerAddress:  "1Address2",
					SignatureValue: base64.StdEncoding.EncodeToString([]byte("signature2")),
					Valid:          true, // We now trust transaction signatures without message
				},
			},
		},
		{
			name: "invalid Sigma protocol (missing fields)",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: SIGMAPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte(string(AlgoECDSA)))
							// Missing address and signature
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*Sigma{},
		},
		{
			name: "Sigma with message field",
			bitcom: &Bitcom{
				Protocols: []*BitcomProtocol{
					{
						Protocol: SIGMAPrefix,
						Script: func() []byte {
							s := &script.Script{}
							_ = s.AppendPushData([]byte(string(AlgoECDSA)))
							_ = s.AppendPushData([]byte("1AddressBTC12345678"))
							_ = s.AppendPushData([]byte("binary-signature-data"))
							_ = s.AppendPushData([]byte("This is the message"))
							return *s
						}(),
						Pos: 0,
					},
				},
			},
			expected: []*Sigma{
				{
					Algorithm:      AlgoECDSA,
					SignerAddress:  "1AddressBTC12345678",
					SignatureValue: base64.StdEncoding.EncodeToString([]byte("binary-signature-data")),
					Message:        "This is the message",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DecodeSIGMA(tt.bitcom)

			if tt.name == "multiple Sigma protocols" {
				t.Logf("Expected %d Sigmas, got %d Sigmas", len(tt.expected), len(result))
				t.Logf("Result: %+v", result)
			}

			require.Equal(t, len(tt.expected), len(result))

			if len(tt.expected) > 0 {
				for i, expectedSigma := range tt.expected {
					if i >= len(result) {
						t.Fatalf("Missing expected Sigma at index %d", i)
						continue
					}

					resultSigma := result[i]
					require.Equal(t, expectedSigma.Algorithm, resultSigma.Algorithm)
					require.Equal(t, expectedSigma.SignerAddress, resultSigma.SignerAddress)
					require.Equal(t, expectedSigma.SignatureValue, resultSigma.SignatureValue)
					require.Equal(t, expectedSigma.Message, resultSigma.Message)
					require.Equal(t, expectedSigma.Nonce, resultSigma.Nonce)
				}
			}
		})
	}
}

// TestGetSignatureBytes tests the GetSignatureBytes function
func TestGetSignatureBytes(t *testing.T) {
	tests := []struct {
		name          string
		sigma         *Sigma
		expected      []byte
		shouldSucceed bool
	}{
		{
			name: "regular signature",
			sigma: &Sigma{
				SignatureValue: base64.StdEncoding.EncodeToString([]byte("test-signature")),
			},
			expected:      []byte("test-signature"),
			shouldSucceed: true,
		},
		{
			name: "empty signature",
			sigma: &Sigma{
				SignatureValue: "",
			},
			expected:      nil,
			shouldSucceed: true,
		},
		{
			name: "invalid base64",
			sigma: &Sigma{
				SignatureValue: "not-valid-base64!@#",
			},
			expected:      nil,
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.sigma.GetSignatureBytes()

			if tt.shouldSucceed {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestDecodeWithSigmaTestVector verifies that the Sigma protocol can properly decode
// the test vector from 34adf92c766e11a656d3ff3508df7b1a31405821bf734bc9bef9fb43fcf701f9.hex
func TestDecodeWithSigmaTestVector(t *testing.T) {
	// Load the hex data from the file
	hexData, err := os.ReadFile("testdata/34adf92c766e11a656d3ff3508df7b1a31405821bf734bc9bef9fb43fcf701f9.hex")
	require.NoError(t, err, "Failed to read hex data from file")

	// Create a transaction from the bytes
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexData)))
	require.NoError(t, err, "Failed to create transaction from bytes")

	// Verify transaction ID matches expected
	expectedTxID := "34adf92c766e11a656d3ff3508df7b1a31405821bf734bc9bef9fb43fcf701f9"
	require.Equal(t, expectedTxID, tx.TxID().String(), "Transaction ID should match expected value")

	// Extract the OP_RETURN output from the transaction
	// The Sigma protocol data should be in an OP_RETURN output
	t.Logf("Transaction has %d outputs", len(tx.Outputs))

	// Inspect all outputs to find where the OP_RETURN data is located
	var bitcomData *Bitcom
	for i, output := range tx.Outputs {
		// Use bitcom to decode each output's script
		decodedBitcom := Decode(output.LockingScript)
		if decodedBitcom != nil && len(decodedBitcom.Protocols) > 0 {
			t.Logf("Output %d has %d BitCom protocols", i, len(decodedBitcom.Protocols))

			// Debug: print all protocols in this output
			for j, proto := range decodedBitcom.Protocols {
				t.Logf("  Protocol %d: %s, Script length: %d", j, proto.Protocol, len(proto.Script))
				t.Logf("  Script hex: %x", proto.Script)
			}

			// Check if any protocol is Sigma
			for _, proto := range decodedBitcom.Protocols {
				if proto.Protocol == SIGMAPrefix {
					bitcomData = decodedBitcom
					t.Logf("Found Sigma protocol in output %d", i)
					break
				}
			}

			if bitcomData != nil {
				break
			}
		}
	}

	require.NotNil(t, bitcomData, "No BitCom data with Sigma protocol found in transaction")

	// Decode the Sigma data
	sigmaData := DecodeSIGMA(bitcomData)

	// Debug: print BitCom protocols to understand the issue
	t.Logf("BitCom has %d protocols", len(bitcomData.Protocols))
	for i, proto := range bitcomData.Protocols {
		t.Logf("Protocol %d: %s, Script length: %d", i, proto.Protocol, len(proto.Script))
		if proto.Protocol == SIGMAPrefix {
			t.Logf("SIGMA protocol found at index %d, Script: %x", i, proto.Script)
		}
	}

	// Debug the actual protocol value vs our constant
	t.Logf("SIGMAPrefix constant value: %q", SIGMAPrefix)
	t.Logf("Actual protocol value:     %q", bitcomData.Protocols[1].Protocol)

	require.NotEmpty(t, sigmaData, "Decoded Sigma data should not be empty")

	// Expected values according to the information provided
	expectedVIN := 0
	expectedAddress := "12KP5KzkBwtsc1UrTrsBCJzgqKn8UqaYQq"
	expectedAlgorithm := "BSM"
	expectedSignature := "H6hhGMaMJ0wkQUjcm2155LyBLc/f6+pRHLcUpUoqssj+dEcqr5yH2scBKSY0Z9RgHd066xRbLCBMPDu29Bu8vPc="

	// Log decoded data for inspection
	for i, sigma := range sigmaData {
		t.Logf("Sigma[%d]: %+v", i, sigma)
	}

	// Verify that there is at least one signature that matches the expected values
	var found bool
	for _, sigma := range sigmaData {
		if sigma.SignerAddress == expectedAddress &&
			sigma.Algorithm == SignatureAlgorithm(expectedAlgorithm) &&
			sigma.SignatureValue == expectedSignature &&
			sigma.VIN == expectedVIN {
			found = true
			t.Logf("Successfully verified Sigma signature: Address=%s, Algorithm=%s, Signature=%s, VIN=%d",
				sigma.SignerAddress, sigma.Algorithm, sigma.SignatureValue, sigma.VIN)
			break
		}
	}

	require.True(t, found, "Did not find a Sigma signature matching the expected values")
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name           string
		sigmaSignature *Sigma
		expectValid    bool
	}{
		{
			name: "Valid BSM signature",
			sigmaSignature: &Sigma{
				Algorithm:     AlgoBSM,
				SignerAddress: "1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz",
				// This is a valid signature for the message "Hello, World!" from address 1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz
				SignatureValue: "H89DSY12iMmrF16T4aDPwFcqrtuGxyoT69yTBH4GqXyzNZ+POVhxV5FLAvHdwKmJ0IhQT/w7JQpTg0XBZ5zeJ+c=",
				Message:        "Hello, World!",
			},
			expectValid: true,
		},
		{
			name: "Invalid BSM signature (wrong signature)",
			sigmaSignature: &Sigma{
				Algorithm:     AlgoBSM,
				SignerAddress: "1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz",
				// This is an invalid signature
				SignatureValue: "H00000012iMmrF16T4aDPwFcqrtuGxyoT69yTBH4GqXyzNZ+POVhxV5FLAvHdwKmJ0IhQT/w7JQpTg0XBZ5zeJ+c=",
				Message:        "Hello, World!",
			},
			expectValid: false,
		},
		{
			name: "Invalid BSM signature (wrong address)",
			sigmaSignature: &Sigma{
				Algorithm:     AlgoBSM,
				SignerAddress: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", // Wrong address
				// This is a valid signature for the message "Hello, World!" from address 1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz
				SignatureValue: "H89DSY12iMmrF16T4aDPwFcqrtuGxyoT69yTBH4GqXyzNZ+POVhxV5FLAvHdwKmJ0IhQT/w7JQpTg0XBZ5zeJ+c=",
				Message:        "Hello, World!",
			},
			expectValid: false,
		},
		{
			name: "Invalid BSM signature (wrong message)",
			sigmaSignature: &Sigma{
				Algorithm:     AlgoBSM,
				SignerAddress: "1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz",
				// This is a valid signature for the message "Hello, World!" from address 1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz
				SignatureValue: "H89DSY12iMmrF16T4aDPwFcqrtuGxyoT69yTBH4GqXyzNZ+POVhxV5FLAvHdwKmJ0IhQT/w7JQpTg0XBZ5zeJ+c=",
				Message:        "Modified message",
			},
			expectValid: false,
		},
		{
			name: "Missing required data",
			sigmaSignature: &Sigma{
				Algorithm:      AlgoBSM,
				SignerAddress:  "", // Missing address
				SignatureValue: "", // Missing signature
				Message:        "", // Missing message
			},
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sigmaSignature.Verify()

			if tt.expectValid {
				assert.NoError(t, err, "Verification should succeed")
				assert.True(t, tt.sigmaSignature.Valid, "Signature should be marked as valid")
			} else {
				if tt.sigmaSignature.SignerAddress == "" || tt.sigmaSignature.SignatureValue == "" || tt.sigmaSignature.Message == "" {
					assert.Error(t, err, "Verification should fail due to missing data")
				} else {
					assert.Error(t, err, "Verification should fail")
					assert.False(t, tt.sigmaSignature.Valid, "Signature should be marked as invalid")
				}
			}
		})
	}
}

func TestDecodeSigmaWithVerification(t *testing.T) {
	// Create a simple Sigma bitcom protocol with a valid signature
	s := &script.Script{}
	_ = s.AppendPushData([]byte("BSM"))
	_ = s.AppendPushData([]byte("1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz"))

	// Decode the valid signature from base64
	sigBytes, err := base64.StdEncoding.DecodeString("H89DSY12iMmrF16T4aDPwFcqrtuGxyoT69yTBH4GqXyzNZ+POVhxV5FLAvHdwKmJ0IhQT/w7JQpTg0XBZ5zeJ+c=")
	require.NoError(t, err)

	_ = s.AppendPushData(sigBytes)
	_ = s.AppendPushData([]byte("Hello, World!")) // Message

	// Create a BitCom protocol
	bc := &Bitcom{
		Protocols: []*BitcomProtocol{
			{
				Protocol: SIGMAPrefix,
				Script:   *s,
			},
		},
	}

	// Decode and verify
	sigmas := DecodeSIGMA(bc)
	require.Len(t, sigmas, 1, "Should decode one Sigma signature")

	// Check that it was validated correctly
	assert.True(t, sigmas[0].Valid, "Signature should be marked as valid")
	assert.Equal(t, "BSM", string(sigmas[0].Algorithm))
	assert.Equal(t, "1EXhSbGFiEAZCE5eeBvUxT6cBVHhrpPWXz", sigmas[0].SignerAddress)
	assert.Equal(t, "Hello, World!", sigmas[0].Message)
}
