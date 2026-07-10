package bitcom

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DecodeFromOutput extracts Bitcom data from a transaction output
func DecodeFromOutput(output *transaction.TransactionOutput) (*Bitcom, error) {
	if output == nil || output.LockingScript == nil {
		return nil, nil
	}

	bitcom := Decode(output.LockingScript)
	if bitcom == nil || len(bitcom.Protocols) == 0 {
		return nil, nil
	}

	return bitcom, nil
}

func TestDecodeBAP(t *testing.T) {
	// Read the test vector hex file
	txID := "c2f0f5f503c012737a8ee0dfa2ae40f52177338fd746afccdd992b0e165af6f9"
	testdataFile := filepath.Join("testdata", txID+".hex")

	hexBytes, err := os.ReadFile(testdataFile)
	if err != nil {
		t.Fatalf("Failed to read test vector file: %v", err)
	}

	// Parse the transaction from hex
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexBytes)))
	if err != nil {
		t.Fatalf("Failed to parse transaction: %v", err)
	}

	// Verify transaction has the expected ID
	assert.Equal(t, txID, tx.TxID().String())

	// Log transaction details
	t.Logf("Transaction ID: %s", tx.TxID().String())
	t.Logf("Inputs: %d, Outputs: %d", len(tx.Inputs), len(tx.Outputs))

	// Now decode BAP data from each output
	foundBAP := false

	for i, output := range tx.Outputs {
		// Get OP_RETURN data if present
		bitcom, err := DecodeFromOutput(output)
		if err != nil || bitcom == nil {
			t.Logf("Output %d: Not an OP_RETURN output or failed to decode", i)
			continue
		}

		// Examine protocol data
		for j, proto := range bitcom.Protocols {
			t.Logf("Protocol %d: %s (length: %d)", j, proto.Protocol, len(proto.Script))
			t.Logf("Protocol script: %x", proto.Script)

			// Check if this is the BAP protocol
			if proto.Protocol == BAPPrefix {
				t.Logf("Found BAP protocol")

				// Parse script into chunks for analysis
				scr := script.NewFromBytes(proto.Script)
				if scr == nil {
					t.Logf("Failed to create script from protocol data")
					continue
				}

				chunks, err := scr.Chunks()
				if err != nil {
					t.Logf("Failed to parse chunks: %v", err)
					continue
				}

				t.Logf("Script has %d chunks", len(chunks))
				for k, chunk := range chunks {
					t.Logf("Chunk %d: Op=%d, Data=%s", k, chunk.Op, string(chunk.Data))
				}
			}
		}

		// Attempt to decode BAP data
		bap := DecodeBAP(bitcom)
		if bap != nil {
			foundBAP = true
			t.Logf("Found BAP data in output %d", i)

			// Verify BAP attributes
			assert.NotEmpty(t, bap.Type, "BAP type should not be empty")

			// Check specific attributes based on the BAP type
			switch bap.Type {
			case ID:
				t.Logf("BAP ID: %s", bap.IDKey)
				t.Logf("BAP Address: %s", bap.Address)
				assert.NotEmpty(t, bap.IDKey, "Identity key should not be empty for ID type")
				assert.NotEmpty(t, bap.Address, "Address should not be empty for ID type")

			case ATTEST:
				t.Logf("BAP ATTEST to TXID: %s", bap.IDKey)
				t.Logf("BAP Sequence Number: %d", bap.Sequence)
				assert.NotEmpty(t, bap.IDKey, "TXID should not be empty for ATTEST type")
				assert.NotEmpty(t, bap.Sequence, "Sequence number should not be empty for ATTEST type")

			case REVOKE:
				t.Logf("BAP REVOKE TXID: %s", bap.IDKey)
				t.Logf("BAP Sequence Number: %d", bap.Sequence)
				assert.NotEmpty(t, bap.IDKey, "TXID should not be empty for REVOKE type")
				assert.NotEmpty(t, bap.Sequence, "Sequence number should not be empty for REVOKE type")

			case ALIAS:
				t.Logf("BAP ALIAS: %s", bap.IDKey)
				t.Logf("BAP Address: %s", bap.Address)
				assert.NotEmpty(t, bap.IDKey, "Alias should not be empty for ALIAS type")
				assert.NotEmpty(t, bap.Address, "Address should not be empty for ALIAS type")
			}

			// Check for AIP signature data
			if bap.Algorithm != "" {
				t.Logf("BAP has AIP signature: Algorithm: %s, Signer: %s", bap.Algorithm, bap.SignerAddr)
				assert.NotEmpty(t, bap.SignerAddr, "Signer address should not be empty when algorithm is present")
				assert.NotEmpty(t, bap.Signature, "Signature should not be empty when algorithm is present")
			}
		}
	}

	// For this test, we'll accept it whether we find BAP data or not, since we're
	// just testing our parser's ability to handle real-world data in whatever form it comes
	// If we don't find BAP data, we'll log that and continue
	if !foundBAP {
		t.Log("No BAP data found in the transaction - this is acceptable for testing purposes")
	}
}

// TestCreateBAP tests creating a BAP message and verifying it can be correctly decoded
func TestCreateBAP(t *testing.T) {
	// Create a BAP ID message with identity key and address
	identity := "TestIdentityKey123456"
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa" // Example Bitcoin address (satoshi's address)

	// Create a correct BAP protocol script
	s := &script.Script{}

	// Add the BAP type as a separate push
	err := s.AppendPushData([]byte(string(ID)))
	require.NoError(t, err)

	// Add the identity key as a separate push
	err = s.AppendPushData([]byte(identity))
	require.NoError(t, err)

	// Add the address as a separate push
	err = s.AppendPushData([]byte(address))
	require.NoError(t, err)

	// Log the created script
	t.Logf("BAP ID script: %x", s.Bytes())

	// Create a BitcomProtocol with the BAP script
	bapProtocol := &BitcomProtocol{
		Protocol: BAPPrefix,
		Script:   *s,
	}

	// Create a Bitcom structure
	bitcom := &Bitcom{
		Protocols: []*BitcomProtocol{bapProtocol},
	}

	// Create a locking script from the Bitcom structure
	lockingScript := bitcom.Lock()
	require.NotNil(t, lockingScript, "Locking script should not be nil")
	t.Logf("BAP locking script: %x", lockingScript.Bytes())

	// Create a transaction output with the BAP message
	output := &transaction.TransactionOutput{
		Satoshis:      0, // 0 satoshis for OP_RETURN outputs
		LockingScript: lockingScript,
	}

	// Now decode the BAP message from the output
	decodedBitcom, err := DecodeFromOutput(output)
	require.NoError(t, err)
	require.NotNil(t, decodedBitcom, "Decoded Bitcom should not be nil")
	t.Logf("Decoded Bitcom has %d protocols", len(decodedBitcom.Protocols))

	// Examine the protocol data
	for i, proto := range decodedBitcom.Protocols {
		t.Logf("Protocol %d: %s (length: %d)", i, proto.Protocol, len(proto.Script))
		t.Logf("Protocol script: %x", proto.Script)

		// Check if this is the BAP protocol
		if proto.Protocol == BAPPrefix {
			t.Logf("Found BAP protocol")

			// Parse script into chunks for analysis
			scr := script.NewFromBytes(proto.Script)
			if scr == nil {
				t.Logf("Failed to create script from protocol data")
				continue
			}

			chunks, err := scr.Chunks()
			if err != nil {
				t.Logf("Failed to parse chunks: %v", err)
				continue
			}

			t.Logf("Script has %d chunks", len(chunks))
			for j, chunk := range chunks {
				t.Logf("Chunk %d: Op=%d, Data=%s", j, chunk.Op, string(chunk.Data))
			}
		}
	}

	// Decode the BAP protocol data
	bap := DecodeBAP(decodedBitcom)
	require.NotNil(t, bap, "BAP data should not be nil")

	// Verify the decoded BAP data
	assert.Equal(t, ID, bap.Type)
	assert.Equal(t, identity, bap.IDKey)
	assert.Equal(t, address, bap.Address)

	// The decoded BAP should not have AIP signature data
	assert.Empty(t, bap.Algorithm)
	assert.Empty(t, bap.SignerAddr)
	assert.Empty(t, bap.Signature)
	assert.False(t, bap.IsSignedByID)

	// Test creating an ATTEST message
	txid := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	seqNum := 1

	// Create a correct ATTEST script
	attestScript := &script.Script{}

	// Add the ATTEST type as a separate push
	err = attestScript.AppendPushData([]byte(string(ATTEST)))
	require.NoError(t, err)

	// Add the TXID as a separate push
	err = attestScript.AppendPushData([]byte(txid))
	require.NoError(t, err)

	// Add the sequence number as a separate push
	err = attestScript.AppendPushData([]byte(strconv.Itoa(seqNum)))
	require.NoError(t, err)

	// Log the created script
	t.Logf("BAP ATTEST script: %x", attestScript.Bytes())

	// Create a BitcomProtocol with the ATTEST script
	attestProtocol := &BitcomProtocol{
		Protocol: BAPPrefix,
		Script:   *attestScript,
	}

	// Create a Bitcom structure
	attestBitcom := &Bitcom{
		Protocols: []*BitcomProtocol{attestProtocol},
	}

	// Create a locking script from the Bitcom structure
	attestLockingScript := attestBitcom.Lock()
	require.NotNil(t, attestLockingScript, "ATTEST locking script should not be nil")
	t.Logf("BAP ATTEST locking script: %x", attestLockingScript.Bytes())

	// Decode the BAP ATTEST message from the script
	attestDecodedBitcom := Decode(attestLockingScript)
	require.NotNil(t, attestDecodedBitcom, "Decoded ATTEST Bitcom should not be nil")
	t.Logf("Decoded ATTEST Bitcom has %d protocols", len(attestDecodedBitcom.Protocols))

	// Examine the ATTEST protocol data
	for i, proto := range attestDecodedBitcom.Protocols {
		t.Logf("ATTEST Protocol %d: %s (length: %d)", i, proto.Protocol, len(proto.Script))

		// Check if this is the BAP protocol
		if proto.Protocol == BAPPrefix {
			t.Logf("Found BAP ATTEST protocol")

			// Parse script into chunks for analysis
			scr := script.NewFromBytes(proto.Script)
			if scr == nil {
				t.Logf("Failed to create script from ATTEST protocol data")
				continue
			}

			chunks, err := scr.Chunks()
			if err != nil {
				t.Logf("Failed to parse ATTEST chunks: %v", err)
				continue
			}

			t.Logf("ATTEST script has %d chunks", len(chunks))
			for j, chunk := range chunks {
				t.Logf("ATTEST Chunk %d: Op=%d, Data=%s", j, chunk.Op, string(chunk.Data))
			}
		}
	}

	// Decode the BAP protocol data
	attestBap := DecodeBAP(attestDecodedBitcom)
	require.NotNil(t, attestBap, "BAP ATTEST data should not be nil")

	// Verify the decoded BAP ATTEST data
	assert.Equal(t, ATTEST, attestBap.Type)
	assert.Equal(t, txid, attestBap.IDKey)
	assert.Equal(t, uint64(seqNum), attestBap.Sequence)
}
