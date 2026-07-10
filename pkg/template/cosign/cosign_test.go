package cosign

import (
	"encoding/hex"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/stretchr/testify/require"
)

// TestCosignLock verifies the Lock function to create a cosigner script
func TestCosignLock(t *testing.T) {
	// Create a test owner key
	ownerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Create a test cosigner key
	cosignerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Get the public keys
	ownerPubKey := ownerKey.PubKey()
	cosignerPubKey := cosignerKey.PubKey()

	// Create address for the owner
	ownerPubKeyHash := ownerPubKey.Compressed()
	ownerAddress, err := script.NewAddressFromPublicKeyHash(ownerPubKeyHash[:20], true)
	require.NoError(t, err)

	// Create the locking script
	lockScript, err := Lock(ownerAddress, cosignerKey.PubKey())
	require.NoError(t, err)

	// Check that the locking script is not nil
	require.NotNil(t, lockScript)

	// Verify structure of the locking script
	chunks, err := lockScript.Chunks()
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	// Log for diagnostic purposes
	t.Logf("Cosign script: %x", *lockScript)

	// Verify we can find the expected components in the script
	require.Equal(t, 7, len(chunks))
	require.Equal(t, script.OpDUP, chunks[0].Op)
	require.Equal(t, script.OpHASH160, chunks[1].Op)
	require.Equal(t, []byte(ownerAddress.PublicKeyHash), chunks[2].Data)
	require.Equal(t, script.OpEQUALVERIFY, chunks[3].Op)
	require.Equal(t, script.OpCHECKSIGVERIFY, chunks[4].Op)
	require.Equal(t, cosignerPubKey.Compressed(), chunks[5].Data)
	require.Equal(t, script.OpCHECKSIG, chunks[6].Op)
}

// TestCosignParseScript verifies the ParseScript function
func TestCosignParseScript(t *testing.T) {
	// Create a test owner key
	ownerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Create a test cosigner key
	cosignerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Get the public keys
	ownerPubKey := ownerKey.PubKey()
	cosignerPubKey := cosignerKey.PubKey()

	// Create address for the owner
	ownerPubKeyHash := ownerPubKey.Compressed()
	ownerAddress, err := script.NewAddressFromPublicKeyHash(ownerPubKeyHash[:20], true)
	require.NoError(t, err)

	// Create the locking script
	lockScript, err := Lock(ownerAddress, cosignerKey.PubKey())
	require.NoError(t, err)

	// Parse the script
	parsed := Decode(lockScript)
	require.NotNil(t, parsed)

	// Verify that the parsed data matches what we expect
	require.Equal(t, ownerAddress.AddressString, parsed.Address)
	require.Equal(t, hex.EncodeToString(cosignerPubKey.Compressed()), parsed.Cosigner)
}

// TestCosignOwnerUnlock verifies the OwnerUnlock function
func TestCosignOwnerUnlock(t *testing.T) {
	// Create a test owner key
	ownerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Create a test cosigner key
	cosignerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Get the public keys
	ownerPubKey := ownerKey.PubKey()

	// Create address for the owner
	ownerPubKeyHash := ownerPubKey.Compressed()
	ownerAddress, err := script.NewAddressFromPublicKeyHash(ownerPubKeyHash[:20], true)
	require.NoError(t, err)

	// Create the locking script
	lockScript, err := Lock(ownerAddress, cosignerKey.PubKey())
	require.NoError(t, err)

	// Create a transaction
	tx := transaction.NewTransaction()
	tx.Version = 1
	tx.LockTime = 0

	// Convert locking script to hex for AddInputFrom
	lockScriptHex := hex.EncodeToString(*lockScript)

	// Add an input using the cosign script
	err = tx.AddInputFrom(
		"1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", // Dummy TXID
		0,             // Output index
		lockScriptHex, // Locking script hex
		100000000,     // 1 BSV in satoshis
		nil,           // No unlocking template
	)
	require.NoError(t, err)

	// Add an output
	p2pkhBytes := make([]byte, 0)
	p2pkhBytes = append(p2pkhBytes, script.OpDUP, script.OpHASH160, byte(script.OpDATA20))
	p2pkhBytes = append(p2pkhBytes, ownerAddress.PublicKeyHash...)
	p2pkhBytes = append(p2pkhBytes, script.OpEQUALVERIFY, script.OpCHECKSIG)
	p2pkhScript := script.Script(p2pkhBytes)

	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      99999000, // Minus fees
		LockingScript: &p2pkhScript,
	})

	// Create a cosign owner unlocker
	shf := sighash.AllForkID
	unlocker, err := OwnerUnlock(ownerKey, &shf)
	require.NoError(t, err)
	require.NotNil(t, unlocker)

	// Estimate the unlocking script length
	estimatedLength := unlocker.EstimateLength(tx, 0)
	require.Greater(t, estimatedLength, uint32(0))
	t.Logf("Estimated unlocking script length: %d", estimatedLength)

	// Sign the transaction
	unlockingScript, err := unlocker.Sign(tx, 0)
	require.NoError(t, err)
	require.NotNil(t, unlockingScript)

	// Set the unlocking script
	tx.Inputs[0].UnlockingScript = unlockingScript

	// Log information for diagnostic purposes
	t.Logf("Transaction signed: %s", tx.String())
	t.Logf("Unlocking script length: %d", len(*unlockingScript))
}

// TestCosignApproverUnlock verifies the ApproverUnlock function
func TestCosignApproverUnlock(t *testing.T) {
	// Create a test owner key
	ownerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Create a test cosigner key
	cosignerKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Get the public keys
	ownerPubKey := ownerKey.PubKey()

	// Create address for the owner
	ownerPubKeyHash := ownerPubKey.Compressed()
	ownerAddress, err := script.NewAddressFromPublicKeyHash(ownerPubKeyHash[:20], true)
	require.NoError(t, err)

	// Create the locking script
	lockScript, err := Lock(ownerAddress, cosignerKey.PubKey())
	require.NoError(t, err)

	// Create a transaction
	tx := transaction.NewTransaction()
	tx.Version = 1
	tx.LockTime = 0

	// Convert locking script to hex for AddInputFrom
	lockScriptHex := hex.EncodeToString(*lockScript)

	// Add an input using the cosign script
	err = tx.AddInputFrom(
		"1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", // Dummy TXID
		0,             // Output index
		lockScriptHex, // Locking script hex
		100000000,     // 1 BSV in satoshis
		nil,           // No unlocking template
	)
	require.NoError(t, err)

	// Add an output
	p2pkhBytes := make([]byte, 0)
	p2pkhBytes = append(p2pkhBytes, script.OpDUP, script.OpHASH160, byte(script.OpDATA20))
	p2pkhBytes = append(p2pkhBytes, ownerAddress.PublicKeyHash...)
	p2pkhBytes = append(p2pkhBytes, script.OpEQUALVERIFY, script.OpCHECKSIG)
	p2pkhScript := script.Script(p2pkhBytes)

	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      99999000, // Minus fees
		LockingScript: &p2pkhScript,
	})

	// Create an owner unlock script for the cosigner to work with
	shf := sighash.AllForkID
	ownerUnlocker, err := OwnerUnlock(ownerKey, &shf)
	require.NoError(t, err)

	ownerScript, err := ownerUnlocker.Sign(tx, 0)
	require.NoError(t, err)

	// Create a cosign approver unlocker
	approverUnlocker, err := ApproverUnlock(cosignerKey, ownerScript, &shf)
	require.NoError(t, err)
	require.NotNil(t, approverUnlocker)

	// Estimate the unlocking script length
	estimatedLength := approverUnlocker.EstimateLength(tx, 0)
	require.Greater(t, estimatedLength, uint32(0))
	t.Logf("Estimated approver unlocking script length: %d", estimatedLength)

	// Sign the transaction
	unlockingScript, err := approverUnlocker.Sign(tx, 0)
	require.NoError(t, err)
	require.NotNil(t, unlockingScript)

	// Set the unlocking script
	tx.Inputs[0].UnlockingScript = unlockingScript

	// Log information for diagnostic purposes
	t.Logf("Transaction approved: %s", tx.String())
	t.Logf("Approver unlocking script length: %d", len(*unlockingScript))
}
