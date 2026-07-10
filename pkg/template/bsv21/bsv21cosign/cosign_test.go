package bsv21cosign

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/b-open-io/1sat-stack/pkg/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

func TestOrdCosignCreateAndDecode(t *testing.T) {
	// Create test keys
	ownerPrivateKey, err := ec.NewPrivateKey()
	require.NoError(t, err, "Failed to create owner private key")

	approverPrivateKey, err := ec.NewPrivateKey()
	require.NoError(t, err, "Failed to create approver private key")

	approverPubKey := approverPrivateKey.PubKey()

	// Create address from owner's public key
	ownerAddress, err := script.NewAddressFromPublicKey(ownerPrivateKey.PubKey(), true)
	require.NoError(t, err, "Failed to create owner address")

	// Create a BSV21 token (deploy+mint operation)
	symbol := "TEST"
	decimals := uint8(2)
	bsv21Token := &bsv21.Bsv21{
		Op:       "deploy+mint",
		Amt:      1000000,
		Symbol:   &symbol,
		Decimals: &decimals,
	}

	// Try creating JSON for this token for debugging
	tokenJSON, err := json.Marshal(bsv21Token)
	require.NoError(t, err, "Failed to marshal token")
	t.Logf("BSV21 token JSON: %s", string(tokenJSON))

	// Create an OrdCosign from the token and cosign data
	ordCosign, err := Create(ownerAddress, approverPubKey, bsv21Token)
	require.NoError(t, err, "Failed to create OrdCosign")
	require.NotNil(t, ordCosign, "OrdCosign should not be nil")

	// Get the address from the OrdCosign
	address, err := script.NewAddressFromString(ordCosign.Cosign.Address)
	require.NoError(t, err)
	require.NotNil(t, address)
	require.Equal(t, ownerAddress.AddressString, address.AddressString, "Address should match")

	// Lock the OrdCosign to create a script
	lockingScript, err := ordCosign.Lock(approverPubKey)
	require.NoError(t, err, "Failed to lock OrdCosign")
	require.NotNil(t, lockingScript, "Locking script should not be nil")

	// Log the locking script for debugging
	t.Logf("OrdCosign locking script: %s", hex.EncodeToString(*lockingScript))

	// Debug: check the script structure
	chunks, _ := lockingScript.Chunks()
	for i, chunk := range chunks {
		if chunk.Op <= script.OpPUSHDATA4 && chunk.Op > 0 {
			t.Logf("Chunk %d: PUSHDATA(%d) %s", i, len(chunk.Data), hex.EncodeToString(chunk.Data))
		} else {
			t.Logf("Chunk %d: Op %d", i, chunk.Op)
		}
	}

	// Debug: check if bsv21.Decode works
	token := bsv21.Decode(lockingScript)
	if token != nil {
		t.Logf("BSV21 token decoded: op=%s, amt=%d", token.Op, token.Amt)
	} else {
		t.Logf("BSV21 token decode FAILED")

		// Try to debug the inscription structure
		insc := inscription.Decode(lockingScript)
		if insc != nil {
			t.Logf("Inscription found but not recognized as BSV21 token")
			t.Logf("  Type: %s", insc.File.Type)
			t.Logf("  Content: %s", string(insc.File.Content))

			// Try to manually unmarshal the content
			var data map[string]interface{}
			if err := json.Unmarshal(insc.File.Content, &data); err != nil {
				t.Logf("  Failed to unmarshal content: %v", err)
			} else {
				t.Logf("  Content parsed as JSON: %+v", data)

				// Check specific fields
				if p, ok := data["p"]; ok {
					t.Logf("  p field: %v", p)
				} else {
					t.Logf("  Missing 'p' field")
				}
			}
		} else {
			t.Logf("Not even recognized as an inscription")
		}

		// Try creating a standalone inscription with the same data
		bsv21JSON, _ := json.Marshal(bsv21Token)
		testInsc := &inscription.Inscription{
			File: inscription.File{
				Content: bsv21JSON,
				Type:    "application/bsv-20",
			},
		}

		// Lock it to a script
		testScript, err := testInsc.Lock()
		require.NoError(t, err)

		// Try to decode it
		testToken := bsv21.Decode(testScript)
		if testToken != nil {
			t.Logf("TEST BSV21 token decoded successfully")
		} else {
			t.Logf("TEST BSV21 token also failed to decode")
		}
	}

	// Decode the locking script back to an OrdCosign directly
	decodedOrdCosign := Decode(lockingScript)
	require.NotNil(t, decodedOrdCosign, "Decoded OrdCosign should not be nil")

	// Verify the decoded data
	require.NotNil(t, decodedOrdCosign.Token, "Decoded token should not be nil")
	require.Equal(t, "deploy+mint", decodedOrdCosign.Token.Op, "Operation should match")
	require.Equal(t, uint64(1000000), decodedOrdCosign.Token.Amt, "Amount should match")
	require.NotNil(t, decodedOrdCosign.Token.Symbol, "Symbol should not be nil")
	require.Equal(t, "TEST", *decodedOrdCosign.Token.Symbol, "Symbol should match")
	require.NotNil(t, decodedOrdCosign.Token.Decimals, "Decimals should not be nil")
	require.Equal(t, uint8(2), *decodedOrdCosign.Token.Decimals, "Decimals should match")

	require.NotNil(t, decodedOrdCosign.Cosign, "Decoded cosign should not be nil")
	require.Equal(t, ownerAddress.AddressString, decodedOrdCosign.Cosign.Address, "Owner address should match")
	require.Equal(t, hex.EncodeToString(approverPubKey.Compressed()), decodedOrdCosign.Cosign.Cosigner, "Cosigner should match")

	// Test creating a transaction with the OrdCosign
	tx := transaction.NewTransaction()
	txID := chainhash.Hash{}
	utxo := &transaction.UTXO{
		TxID:          &txID,
		Vout:          0,
		LockingScript: lockingScript,
		Satoshis:      1000,
	}

	// Create unlocker
	unlocker, err := ordCosign.ToUnlocker(ownerPrivateKey, approverPrivateKey, nil)
	require.NoError(t, err, "Failed to create unlocker")

	utxo.UnlockingScriptTemplate = unlocker
	_ = tx.AddInputsFromUTXOs(utxo)

	// Add a simple output
	outputAddress, _ := script.NewAddressFromPublicKey(ownerPrivateKey.PubKey(), true)
	lockingScriptOutput, _ := p2pkh.Lock(outputAddress)
	tx.AddOutput(&transaction.TransactionOutput{
		LockingScript: lockingScriptOutput,
		Satoshis:      900, // Leave some for fees
	})

	// Test that we can create unlocking templates
	ownerTemplate, err := ordCosign.OwnerUnlock(ownerPrivateKey, nil)
	require.NoError(t, err, "Failed to create owner unlock template")
	require.NotNil(t, ownerTemplate, "Owner template should not be nil")

	ownerScript, err := ownerTemplate.Sign(tx, 0)
	require.NoError(t, err, "Failed to sign with owner template")
	require.NotNil(t, ownerScript, "Owner signature script should not be nil")

	approverTemplate, err := ordCosign.ApproverUnlock(approverPrivateKey, ownerScript, nil)
	require.NoError(t, err, "Failed to create approver unlock template")
	require.NotNil(t, approverTemplate, "Approver template should not be nil")

	finalScript, err := approverTemplate.Sign(tx, 0)
	require.NoError(t, err, "Failed to sign with approver template")
	require.NotNil(t, finalScript, "Final signature script should not be nil")

	// Log the transaction details for debugging
	t.Logf("Transaction: %+v", tx)

	// Verify the owner's address
	ownerAddr, err := script.NewAddressFromString(decodedOrdCosign.Cosign.Address)
	require.NoError(t, err)
	require.Equal(t, ownerAddress.AddressString, ownerAddr.AddressString, "Owner address should match")
}

func TestOrdCosignFromExistingInscription(t *testing.T) {
	// Create test keys
	ownerPrivateKey, err := ec.NewPrivateKey()
	require.NoError(t, err, "Failed to create owner private key")

	approverPrivateKey, err := ec.NewPrivateKey()
	require.NoError(t, err, "Failed to create approver private key")

	approverPubKey := approverPrivateKey.PubKey()

	// Create address from owner's public key
	ownerAddress, err := script.NewAddressFromPublicKey(ownerPrivateKey.PubKey(), true)
	require.NoError(t, err, "Failed to create owner address")

	// Create an inscription with BSV-20 content
	insc := &inscription.Inscription{
		File: inscription.File{
			Content: []byte(`{"p":"bsv-20","op":"deploy+mint","sym":"TEST","dec":"2","amt":"1000000"}`),
			Type:    "application/bsv-20",
		},
	}

	// Create BSV21 token from the inscription
	bsv21Token := &bsv21.Bsv21{
		Insc: insc,
		Op:   "deploy+mint",
		Amt:  1000000,
	}

	symbol := "TEST"
	decimals := uint8(2)
	bsv21Token.Symbol = &symbol
	bsv21Token.Decimals = &decimals

	// Create an OrdCosign from the token and cosign data
	ordCosign, err := Create(ownerAddress, approverPubKey, bsv21Token)
	require.NoError(t, err, "Failed to create OrdCosign with existing inscription")

	// Lock the OrdCosign to create a script
	lockingScript, err := ordCosign.Lock(approverPubKey)
	require.NoError(t, err, "Failed to lock OrdCosign with existing inscription")

	// Decode the locking script back to an OrdCosign directly
	decodedOrdCosign := Decode(lockingScript)
	require.NotNil(t, decodedOrdCosign, "Decoded OrdCosign should not be nil")

	// Verify the inscription data is preserved
	require.NotNil(t, decodedOrdCosign.Token, "Decoded token should not be nil")
	require.NotNil(t, decodedOrdCosign.Token.Insc, "Decoded inscription should not be nil")
	require.Equal(t, "application/bsv-20", decodedOrdCosign.Token.Insc.File.Type, "File type should match")
}

// TestDecodeMNEEToken tests decoding the MNEE token which is a BSV21 token with cosign
func TestDecodeMNEEToken(t *testing.T) {
	// Load the test vector hex data for MNEE token transfer with cosign
	hexData, err := os.ReadFile("../testdata/f7ca34a9c0319bfb837a56ee7375e8246229f5fefbdaaaf9fdec97493d428bee.hex")
	require.NoError(t, err, "Failed to read MNEE transfer test vector hex data")

	// Create a transaction from the hex data
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexData)))
	require.NoError(t, err, "Failed to create transaction from hex data")

	// Verify the transaction ID
	expectedTxID := "f7ca34a9c0319bfb837a56ee7375e8246229f5fefbdaaaf9fdec97493d428bee"
	require.Equal(t, expectedTxID, tx.TxID().String(), "Transaction ID should match the expected value")

	// Log transaction info
	t.Logf("Transaction ID: %s", tx.TxID().String())
	t.Logf("Transaction has %d inputs and %d outputs", len(tx.Inputs), len(tx.Outputs))

	// Try to decode each output as a BSV21Cosign
	var ordCosign *OrdCosign
	var foundOutput int = -1

	for i, output := range tx.Outputs {
		t.Logf("Checking output %d with %d satoshis", i, output.Satoshis)

		// Try to decode as OrdCosign
		ordCosign = Decode(output.LockingScript)
		if ordCosign != nil {
			foundOutput = i
			t.Logf("Found BSV21Cosign data in output %d", i)
			break
		}
	}

	// Make sure we found a BSV21Cosign
	require.NotEqual(t, -1, foundOutput, "Should find a BSV21Cosign in one of the outputs")
	require.NotNil(t, ordCosign, "BSV21Cosign data should not be nil")
	require.NotNil(t, ordCosign.Token, "BSV21 token should not be nil")

	// Check the decoded BSV21 token
	t.Logf("BSV21 data: Op=%s, Amt=%d", ordCosign.Token.Op, ordCosign.Token.Amt)
	if ordCosign.Token.Symbol != nil {
		t.Logf("BSV21 Symbol: %s", *ordCosign.Token.Symbol)
	}
	if ordCosign.Token.Decimals != nil {
		t.Logf("BSV21 Decimals: %d", *ordCosign.Token.Decimals)
	}
	if ordCosign.Token.Icon != nil {
		t.Logf("BSV21 Icon: %s", *ordCosign.Token.Icon)
	}

	// Check the decoded cosign data
	require.NotNil(t, ordCosign.Cosign, "Cosign data should not be nil")
	t.Logf("Cosign address: %s", ordCosign.Cosign.Address)
	require.NotEmpty(t, ordCosign.Cosign.Cosigner, "Cosigner should not be empty")
	t.Logf("Cosign cosigner: %s", ordCosign.Cosign.Cosigner)

	// Add a test for the specific expected MNEE token transfer characteristics
	require.Equal(t, "transfer", ordCosign.Token.Op, "Operation should be 'transfer'")
	require.NotNil(t, ordCosign.Token.Id, "Token ID should not be nil")
	t.Logf("Token ID: %s", ordCosign.Token.Id)
}
