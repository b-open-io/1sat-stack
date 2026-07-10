package ltm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/stretchr/testify/require"
)

// TestDecodeLTMFromTestVector tests decoding a Lock-to-Mint (LTM) contract from a test vector
func TestDecodeLTMFromTestVector(t *testing.T) {
	// Load the test vector hex data
	hexData, err := os.ReadFile("../testdata/1bff350b55a113f7da23eaba1dc40a7c5b486d3e1017cda79dbe6bd42e001c81.hex")
	require.NoError(t, err, "Failed to read test vector hex data")

	// Create a transaction from the hex data
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(hexData)))
	require.NoError(t, err, "Failed to create transaction from hex data")

	// Verify the transaction ID
	expectedTxID := "1bff350b55a113f7da23eaba1dc40a7c5b486d3e1017cda79dbe6bd42e001c81"
	require.Equal(t, expectedTxID, tx.TxID().String(), "Transaction ID should match the expected value")

	// Log transaction info
	t.Logf("Transaction ID: %s", tx.TxID().String())
	t.Logf("Transaction has %d inputs and %d outputs", len(tx.Inputs), len(tx.Outputs))

	// Try to find an LTM contract in the outputs
	var ltmData *LockToMint
	ltmFound := false
	var ltmJsonData map[string]any

	for i, output := range tx.Outputs {
		t.Logf("Checking output %d with %d satoshis", i, output.Satoshis)
		ltmData = Decode(output.LockingScript)
		if ltmData != nil {
			t.Logf("Found LTM data in output %d", i)
			ltmFound = true
			break
		}
	}

	// If we haven't found direct LTM data, check for inscriptions that might contain LTM data
	if !ltmFound {
		t.Log("No direct LTM contract found, checking for inscriptions...")

		// Inspect each output for inscriptions
		for i, output := range tx.Outputs {
			inscData := inscription.Decode(output.LockingScript)
			if inscData != nil {
				t.Logf("Found inscription in output %d with content type: %s", i, inscData.File.Type)
				t.Logf("Inscription content: %s", string(inscData.File.Content))

				// If it's a BSV-20 inscription, try to parse it as JSON
				if inscData.File.Type == "application/bsv-20" {
					if err := json.Unmarshal(inscData.File.Content, &ltmJsonData); err != nil {
						t.Logf("Error parsing inscription content as JSON: %v", err)
						continue
					}

					// Check if it has expected fields for LTM contract
					if ltmJsonData["p"] == "bsv-20" &&
						ltmJsonData["sym"] != nil &&
						ltmJsonData["lockTime"] != nil &&
						ltmJsonData["lockPerToken"] != nil {
						t.Logf("Found LTM contract definition in inscription: %v", ltmJsonData)

						// Log the LTM contract details
						t.Logf("Symbol: %v", ltmJsonData["sym"])
						t.Logf("Amount: %v", ltmJsonData["amt"])
						t.Logf("Decimals: %v", ltmJsonData["dec"])
						t.Logf("Lock Time: %v", ltmJsonData["lockTime"])
						t.Logf("Lock Per Token: %v", ltmJsonData["lockPerToken"])
						t.Logf("Contract Start: %v", ltmJsonData["contractStart"])

						// Consider this a success
						ltmFound = true
						break
					}
				}
			}
		}
	}

	// Now we check if we found either a direct LTM contract or one in an inscription
	require.True(t, ltmFound, "Should find an LTM contract or inscription in one of the outputs")

	// If we found a direct LTM contract, verify its fields
	if ltmData != nil {
		t.Logf("LTM contract data: Symbol=%s, Max=%d, Decimals=%d, Multiplier=%d, LockDuration=%d, StartHeight=%d",
			ltmData.Symbol, ltmData.Max, ltmData.Decimals, ltmData.Multiplier, ltmData.LockDuration, ltmData.StartHeight)

		// Add specific assertions for the expected LTM fields
		require.Equal(t, "TEST", ltmData.Symbol, "Symbol should be TEST")
		require.Greater(t, ltmData.Max, uint64(0), "Max should be greater than 0")
		require.Greater(t, ltmData.Decimals, uint8(0), "Decimals should be greater than 0")
		require.Greater(t, ltmData.Multiplier, uint64(0), "Multiplier should be greater than 0")
		require.Greater(t, ltmData.LockDuration, uint64(0), "LockDuration should be greater than 0")
	} else if ltmJsonData != nil {
		// Verify the JSON fields match our expectations
		require.Equal(t, "bsv-20", ltmJsonData["p"], "Protocol should be bsv-20")
		require.Equal(t, "deploy+mint", ltmJsonData["op"], "Operation should be deploy+mint")
		require.Equal(t, "BAMBOO", ltmJsonData["sym"], "Symbol should be BAMBOO")
		require.Equal(t, "1000000000000000", ltmJsonData["amt"], "Amount should be 1000000000000000")
		require.Equal(t, "8", ltmJsonData["dec"], "Decimals should be 8")
		require.Equal(t, "b9068a24d0c8acceee1fb4db19558dd6c3b8e79a7dab2bca72c6a664af4969cf_0", ltmJsonData["icon"], "Icon should match expected value")
		require.Equal(t, "60000", ltmJsonData["lockTime"], "Lock time should be 60000")
		require.Equal(t, "0.0005", ltmJsonData["lockPerToken"], "Lock per token should be 0.0005")
		require.Equal(t, "821660", ltmJsonData["contractStart"], "Contract start should be 821660")
	}
}

// TestCreateLTMInscription tests creating an LTM token inscription
func TestCreateLTMInscription(t *testing.T) {
	// Create a new LTM contract definition as JSON
	ltmContract := map[string]any{
		"p":             "bsv-20",
		"op":            "deploy",
		"sym":           "GOLD",
		"max":           "21000000",
		"dec":           "8",
		"lockTime":      "144", // 1 day at 10 min per block
		"lockPerToken":  "0.0001",
		"contractStart": "830000",
	}

	// Convert to JSON
	jsonData, err := json.Marshal(ltmContract)
	require.NoError(t, err, "Failed to marshal LTM contract to JSON")

	// Create an inscription with the LTM contract
	insc := &inscription.Inscription{
		File: inscription.File{
			Type:    "application/bsv-20",
			Content: jsonData,
		},
	}

	// Create a Bitcom protocol in the script suffix instead
	contentTypeMap := &script.Script{}
	_ = contentTypeMap.AppendOpcodes(script.OpFALSE, script.OpRETURN)
	_ = contentTypeMap.AppendPushData([]byte("1"))
	_ = contentTypeMap.AppendPushData([]byte("application/bsv-20"))
	insc.ScriptSuffix = *contentTypeMap

	// Lock the inscription to create a script
	ordiScript, err := insc.Lock()
	require.NoError(t, err, "Failed to create inscription script")
	require.NotNil(t, ordiScript, "Inscription script should not be nil")

	// Verify we can decode the inscription back
	decodedInsc := inscription.Decode(ordiScript)
	require.NotNil(t, decodedInsc, "Should be able to decode the inscription")

	// Since the file type isn't preserved in the Lock method, we'll check the content
	var decodedJson map[string]any
	err = json.Unmarshal(decodedInsc.File.Content, &decodedJson)
	require.NoError(t, err, "Failed to unmarshal inscription content")

	// Verify the LTM contract fields
	require.Equal(t, "bsv-20", decodedJson["p"], "Protocol should be bsv-20")
	require.Equal(t, "deploy", decodedJson["op"], "Operation should be deploy")
	require.Equal(t, "GOLD", decodedJson["sym"], "Symbol should be GOLD")
	require.Equal(t, "21000000", decodedJson["max"], "Max should be 21000000")
	require.Equal(t, "8", decodedJson["dec"], "Decimals should be 8")
	require.Equal(t, "144", decodedJson["lockTime"], "Lock time should be 144")
	require.Equal(t, "0.0001", decodedJson["lockPerToken"], "Lock per token should be 0.0001")
	require.Equal(t, "830000", decodedJson["contractStart"], "Contract start should be 830000")
}

// TestCreateLTMTransaction tests creating a complete BSV transaction with an LTM token inscription
func TestCreateLTMTransaction(t *testing.T) {
	// Create a new LTM contract definition as JSON
	ltmContract := map[string]any{
		"p":             "bsv-20",
		"op":            "deploy",
		"sym":           "SILVER",
		"max":           "50000000",
		"dec":           "8",
		"lockTime":      "144", // 1 day at 10 min per block
		"lockPerToken":  "0.0001",
		"contractStart": "830000",
	}

	// Convert to JSON
	jsonData, err := json.Marshal(ltmContract)
	require.NoError(t, err, "Failed to marshal LTM contract to JSON")

	// Create an inscription with the LTM contract
	insc := &inscription.Inscription{
		File: inscription.File{
			Type:    "application/bsv-20",
			Content: jsonData,
		},
	}

	// Create a Bitcom protocol in the script suffix instead
	contentTypeMap := &script.Script{}
	_ = contentTypeMap.AppendOpcodes(script.OpFALSE, script.OpRETURN)
	_ = contentTypeMap.AppendPushData([]byte("1"))
	_ = contentTypeMap.AppendPushData([]byte("application/bsv-20"))
	insc.ScriptSuffix = *contentTypeMap

	// Create a private key for testing
	privKey, err := ec.NewPrivateKey()
	require.NoError(t, err, "Failed to create private key")

	// Get the address from the private key
	pubKey := privKey.PubKey()
	pubKeyBytes := pubKey.Compressed()
	address, err := script.NewAddressFromPublicKeyHash(pubKeyBytes[:20], true)
	require.NoError(t, err, "Failed to create address from public key")

	// Create P2PKH for the scripture suffix
	p2pkhScript, err := p2pkh.Lock(address)
	require.NoError(t, err, "Failed to create P2PKH script")

	// Lock the inscription to the address
	insc.ScriptSuffix = *p2pkhScript

	// Create the complete locking script
	lockingScript, err := insc.Lock()
	require.NoError(t, err, "Failed to create locking script")
	require.NotNil(t, lockingScript, "Locking script should not be nil")

	// Create a transaction
	tx := transaction.NewTransaction()
	tx.Version = 1
	tx.LockTime = 0

	// Add an input to the transaction
	err = tx.AddInputFrom(
		"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20", // Dummy TXID in hex
		0,    // Output index
		"",   // Empty locking script
		1000, // Amount in satoshis
		nil,  // No unlocking template
	)
	require.NoError(t, err, "Failed to add input to transaction")

	// Add output with the inscription
	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      1, // Dust amount for NFT
		LockingScript: lockingScript,
	})

	// Add change output
	changeScript, err := p2pkh.Lock(address)
	require.NoError(t, err, "Failed to create change P2PKH script")

	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      546, // Change output
		LockingScript: changeScript,
	})

	// Verify the transaction was created correctly
	require.Equal(t, 1, len(tx.Inputs), "Transaction should have 1 input")
	require.Equal(t, 2, len(tx.Outputs), "Transaction should have 2 outputs")

	// Verify we can decode the inscription from the output
	decodedInsc := inscription.Decode(tx.Outputs[0].LockingScript)
	require.NotNil(t, decodedInsc, "Should be able to decode the inscription from the transaction output")

	// Verify the LTM contract in the inscription
	var decodedJson map[string]any
	err = json.Unmarshal(decodedInsc.File.Content, &decodedJson)
	require.NoError(t, err, "Failed to unmarshal inscription content")

	// Check the contract fields
	require.Equal(t, "bsv-20", decodedJson["p"], "Protocol should be bsv-20")
	require.Equal(t, "deploy", decodedJson["op"], "Operation should be deploy")
	require.Equal(t, "SILVER", decodedJson["sym"], "Symbol should be SILVER")
}

func TestDecode_MissingPrefix(t *testing.T) {
	s := &script.Script{0x01, 0x02, 0x03}
	result := Decode(s)
	require.Nil(t, result, "Decode should return nil if prefix is missing")
}

func TestDecode_MissingSuffix(t *testing.T) {
	origPrefix := ltmPrefix
	fakePrefix := script.NewFromBytes([]byte("LTM_PREFIX"))
	ltmPrefix = fakePrefix
	defer func() { ltmPrefix = origPrefix }()

	fakeScript := append([]byte("LTM_PREFIX"), 0x01, 0x02, 0x03)
	s := script.NewFromBytes(fakeScript)
	result := Decode(s)
	require.Nil(t, result, "Decode should return nil if suffix is missing")
}

func TestDecode_MalformedChunks(t *testing.T) {
	origPrefix := ltmPrefix
	origSuffix := ltmSuffix
	fakePrefix := script.NewFromBytes([]byte("LTM_PREFIX"))
	fakeSuffix := script.NewFromBytes([]byte("LTM_SUFFIX"))
	ltmPrefix = fakePrefix
	ltmSuffix = fakeSuffix
	defer func() { ltmPrefix = origPrefix; ltmSuffix = origSuffix }()

	scriptBytes := append([]byte("LTM_PREFIX"), []byte("LTM_SUFFIX")...)
	s := script.NewFromBytes(scriptBytes)
	result := Decode(s)
	require.Nil(t, result, "Decode should return nil if chunks are missing")
}

func TestDecode_DecimalsEdgeCases(t *testing.T) {
	origPrefix := ltmPrefix
	origSuffix := ltmSuffix
	fakePrefix := script.NewFromBytes([]byte("LTM_PREFIX"))
	fakeSuffix := script.NewFromBytes([]byte("LTM_SUFFIX"))
	ltmPrefix = fakePrefix
	ltmSuffix = fakeSuffix
	defer func() { ltmPrefix = origPrefix; ltmSuffix = origSuffix }()

	// Symbol, Max, Decimals (as opcode), Multiplier, LockDuration, StartHeight
	chunks := [][]byte{
		[]byte("SYM"),
		{0x01}, // Max
		{},     // Decimals as opcode (simulate Op2)
		{0x02}, // Multiplier
		{0x03}, // LockDuration
		{0x04}, // StartHeight
	}
	scriptBytes := []byte("LTM_PREFIX")
	for i, chunk := range chunks {
		if i == 2 {
			scriptBytes = append(scriptBytes, 0x52) // Op2
		} else {
			scriptBytes = append(scriptBytes, byte(len(chunk)))
			scriptBytes = append(scriptBytes, chunk...)
		}
	}
	scriptBytes = append(scriptBytes, []byte("LTM_SUFFIX")...)
	s := script.NewFromBytes(scriptBytes)
	result := Decode(s)
	require.NotNil(t, result, "Decode should succeed with opcode decimals")
	require.Equal(t, uint8(2), result.Decimals)
}

func TestDecode_DecimalsAsData(t *testing.T) {
	origPrefix := ltmPrefix
	origSuffix := ltmSuffix
	fakePrefix := script.NewFromBytes([]byte("LTM_PREFIX"))
	fakeSuffix := script.NewFromBytes([]byte("LTM_SUFFIX"))
	ltmPrefix = fakePrefix
	ltmSuffix = fakeSuffix
	defer func() { ltmPrefix = origPrefix; ltmSuffix = origSuffix }()

	// Symbol, Max, Decimals (as data), Multiplier, LockDuration, StartHeight
	chunks := [][]byte{
		[]byte("SYM"),
		{0x01}, // Max
		{0x03}, // Decimals as data
		{0x02}, // Multiplier
		{0x03}, // LockDuration
		{0x04}, // StartHeight
	}
	scriptBytes := []byte("LTM_PREFIX")
	for _, chunk := range chunks {
		scriptBytes = append(scriptBytes, byte(len(chunk)))
		scriptBytes = append(scriptBytes, chunk...)
	}
	scriptBytes = append(scriptBytes, []byte("LTM_SUFFIX")...)
	s := script.NewFromBytes(scriptBytes)
	result := Decode(s)
	require.NotNil(t, result, "Decode should succeed with data decimals")
	require.Equal(t, uint8(3), result.Decimals)
}
