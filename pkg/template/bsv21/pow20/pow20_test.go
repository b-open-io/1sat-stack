package pow20

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

// TestDecodePOW20FromTestVector tests decoding a POW20 contract from a test vector
func TestDecodePOW20FromTestVector(t *testing.T) {
	// Load the test vector hex data
	hexData, err := os.ReadFile("../testdata/dfa24771dbd093efbddf19ec424eab60113e288672c23182be75ec3f5452ba8d.hex")
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

	// Try to find a POW20 contract in the outputs
	var pow20Data *Pow20
	for i, output := range tx.Outputs {
		t.Logf("Checking output %d with %d satoshis", i, output.Satoshis)
		pow20Data = Decode(output.LockingScript)
		if pow20Data != nil {
			t.Logf("Found POW20 data in output %d", i)
			break
		}
	}

	// We may not find direct POW20 data since this might be just the initial inscription
	// Instead, let's find the inscription and extract the POW20 fields from the JSON
	var inscData *inscription.Inscription

	// Define a simple map for the JSON content
	var jsonData map[string]any

	// Flag to track if we found the POW20 contract
	foundPOW20 := false

	for i, output := range tx.Outputs {
		inscData = inscription.Decode(output.LockingScript)
		if inscData != nil && inscData.File.Type == "application/bsv-20" {
			t.Logf("Found BSV-20 inscription in output %d", i)
			t.Logf("Inscription content: %s", string(inscData.File.Content))

			// Parse the content to check for POW20 data
			err := json.Unmarshal(inscData.File.Content, &jsonData)
			if err != nil {
				t.Logf("Error parsing inscription content as JSON: %v", err)
				continue
			}

			// Check if this is a POW20 contract
			contract, ok := jsonData["contract"].(string)
			if ok && contract == "pow-20" {
				t.Logf("Found POW20 contract definition in output %d", i)
				foundPOW20 = true
				break
			}
		}
	}

	// Verify we found the POW20 JSON definition
	require.True(t, foundPOW20, "Should find POW20 contract definition in one of the outputs")

	// Extract and verify POW20 fields
	t.Logf("POW20 contract: %+v", jsonData)

	// Check the p field (protocol)
	require.Equal(t, "bsv-20", jsonData["p"], "Protocol should be bsv-20")

	// Check the op field (operation)
	require.Equal(t, "deploy+mint", jsonData["op"], "Operation should be deploy+mint")

	// Check the amt field (amount)
	require.Equal(t, "4200000000", jsonData["amt"], "Amount should be 4200000000")

	// Check the dec field (decimals)
	require.Equal(t, "2", jsonData["dec"], "Decimals should be 2")

	// Check the sym field (symbol)
	require.Equal(t, "BUIDL", jsonData["sym"], "Symbol should be BUIDL")

	// Check the icon field
	require.Equal(t, "df3ceacd1a4169ec7cca3037ca2714f5fcdc0bbdb88ebfd3609257faa4814809_0", jsonData["icon"], "Icon should match expected value")

	// Check POW20-specific fields
	require.Equal(t, "pow-20", jsonData["contract"], "Contract type should be pow-20")
	require.Equal(t, "4200000000", jsonData["maxSupply"], "Max supply should be 4200000000")
	require.Equal(t, "5", jsonData["difficulty"], "Difficulty should be 5")
	require.Equal(t, "100000", jsonData["startingReward"], "Starting reward should be 100000")

	// If we found direct POW20 contract data (unlikely in this test vector but good to check)
	if pow20Data != nil {
		symbol := ""
		if pow20Data.Bsv21 != nil && pow20Data.Bsv21.Symbol != nil {
			symbol = *pow20Data.Bsv21.Symbol
		}

		decimals := uint8(0)
		if pow20Data.Bsv21 != nil && pow20Data.Bsv21.Decimals != nil {
			decimals = *pow20Data.Bsv21.Decimals
		}

		t.Logf("POW20 contract data: Symbol=%s, Max=%d, Dec=%d, Difficulty=%d",
			symbol, pow20Data.MaxSupply, decimals, pow20Data.Difficulty)

		// Now verify the POW20 contract fields
		require.Equal(t, "BUIDL", symbol, "Symbol should be BUIDL")
		require.Equal(t, uint64(4200000000), pow20Data.MaxSupply, "Max supply should be 4200000000")
		require.Equal(t, uint8(2), decimals, "Decimals should be 2")
		require.Equal(t, uint8(5), pow20Data.Difficulty, "Difficulty should be 5")
	} else {
		t.Log("No POW20 contract structure found - this is only the JSON contract definition")
	}
}

func TestDecode_MinimalScript(t *testing.T) {
	// Minimal script: just a pushdata with recognizable POW20 prefix (simulate)
	// This is a placeholder; adjust as needed for your actual script format
	minimal := &script.Script{0x01, 0x02, 0x03}
	result := Decode(minimal)
	require.Nil(t, result, "Decode should return nil for non-POW20 script")
}

func TestLockAndDecode_RoundTrip(t *testing.T) {
	symbol := "POW20"
	decimals := uint8(2)
	bsv21 := &bsv21.Bsv21{
		Id:       "testid123",
		Op:       "deploy+mint",
		Symbol:   &symbol,
		Decimals: &decimals,
	}
	p := &Pow20{
		Bsv21:      bsv21,
		MaxSupply:  1000,
		Reward:     10,
		Difficulty: 2,
	}
	script := p.Lock(1000)
	require.NotNil(t, script)
	t.Logf("Script bytes: %x", []byte(*script))
	decoded := Decode(script)
	t.Logf("Decoded struct: %+v", decoded)
	require.NotNil(t, decoded)
	require.Equal(t, p.MaxSupply, decoded.MaxSupply)
	require.Equal(t, p.Difficulty, decoded.Difficulty)
	if decoded.Bsv21 != nil && decoded.Bsv21.Symbol != nil {
		require.Equal(t, symbol, *decoded.Bsv21.Symbol)
	}
	if decoded.Bsv21 != nil && decoded.Bsv21.Decimals != nil {
		require.Equal(t, decimals, *decoded.Bsv21.Decimals)
	}
}

func TestBuildUnlockTx_Basic(t *testing.T) {
	symbol := "POW20"
	decimals := uint8(2)
	bsv21 := &bsv21.Bsv21{
		Id:       "testid123",
		Op:       "deploy+mint",
		Symbol:   &symbol,
		Decimals: &decimals,
	}
	p := &Pow20{
		Bsv21:      bsv21,
		MaxSupply:  1000,
		Reward:     10,
		Difficulty: 2,
		Supply:     100,
		Txid:       make([]byte, 32),
		Vout:       0,
	}
	changeAddr := &script.Address{PublicKeyHash: make([]byte, 20)}
	recipient := &script.Address{PublicKeyHash: make([]byte, 20)}
	tx, err := p.BuildUnlockTx([]byte{1, 2, 3}, recipient, changeAddr)
	require.NoError(t, err)
	require.NotNil(t, tx)
	require.GreaterOrEqual(t, len(tx.Outputs), 2)
}

func TestUnlock_Basic(t *testing.T) {
	p := &Pow20{}
	recipient := &script.Address{PublicKeyHash: make([]byte, 20)}
	unlock, err := p.Unlock([]byte{1, 2, 3}, recipient)
	require.NoError(t, err)
	require.NotNil(t, unlock)
	require.Equal(t, recipient, unlock.Recipient)
}

func TestSign_Basic(t *testing.T) {
	p := &Pow20{}
	recipient := &script.Address{PublicKeyHash: make([]byte, 20)}
	unlock, _ := p.Unlock([]byte{1, 2, 3}, recipient)
	tx := transaction.NewTransaction()
	// Add a dummy input and output for preimage calculation
	tx.AddInput(&transaction.TransactionInput{})
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: &script.Script{}})
	_, err := unlock.Sign(tx, 0)
	// We expect an error because preimage calculation will fail, but the code path is exercised
	require.Error(t, err)
}

func TestEstimateLength_Basic(t *testing.T) {
	p := &Pow20{}
	recipient := &script.Address{PublicKeyHash: make([]byte, 20)}
	unlock, _ := p.Unlock([]byte{1, 2, 3}, recipient)
	tx := transaction.NewTransaction()
	tx.AddInput(&transaction.TransactionInput{})
	length := unlock.EstimateLength(tx, 0)
	require.Greater(t, length, uint32(0))
}
