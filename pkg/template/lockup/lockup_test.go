package lockup

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/stretchr/testify/require"
)

// TestLockPrefixSuffix verifies that the LockPrefix and LockSuffix constants are set
func TestLockPrefixSuffix(t *testing.T) {
	require.NotNil(t, LockPrefix)
	require.NotNil(t, LockSuffix)
	require.True(t, len(LockPrefix) > 0)
	require.True(t, len(LockSuffix) > 0)

	// Log the lengths for diagnostic purposes
	t.Logf("LockPrefix length: %d", len(LockPrefix))
	t.Logf("LockSuffix length: %d", len(LockSuffix))
}

// TestLockCreate verifies the Lock creation functionality
func TestLockCreate(t *testing.T) {
	// Create a test private key
	privKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Get the public key hash
	pubKey := privKey.PubKey()
	pubKeyBytes := pubKey.Compressed()

	// Create address from public key hash
	address, err := script.NewAddressFromPublicKeyHash(pubKeyBytes[:20], true)
	require.NoError(t, err)

	// Create a lock for 1 hour in the future
	lockTime := uint32(time.Now().Unix()) + 3600
	lock := &Lock{
		Address: address,
		Until:   lockTime,
	}

	// Create the locking script
	lockScript := lock.Lock()
	require.NotNil(t, lockScript)

	// Log script for diagnostic purposes
	t.Logf("Lock script created: %x", *lockScript)

	// Verify the script format - check prefix at the beginning and suffix at the end
	scriptBytes := *lockScript
	require.True(t, bytes.HasPrefix(scriptBytes, LockPrefix), "Script should start with LockPrefix")
	require.True(t, bytes.HasSuffix(scriptBytes, LockSuffix), "Script should end with LockSuffix")
}

// TestLockDecode verifies the Decode functionality
func TestLockDecode(t *testing.T) {
	// Create a test private key
	privKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Get the public key hash
	pubKey := privKey.PubKey()
	pubKeyBytes := pubKey.Compressed()

	// Create address from public key hash
	address, err := script.NewAddressFromPublicKeyHash(pubKeyBytes[:20], true)
	require.NoError(t, err)

	// Create a lock for 1 hour in the future
	lockTime := uint32(time.Now().Unix()) + 3600
	lock := &Lock{
		Address: address,
		Until:   lockTime,
	}

	// Create the locking script
	lockScript := lock.Lock()
	require.NotNil(t, lockScript)

	// Now decode the script
	decodedLock := Decode(lockScript, true)
	require.NotNil(t, decodedLock)

	// Verify the decoded values match what we put in
	require.Equal(t, address.AddressString, decodedLock.Address.AddressString)
	require.Equal(t, lockTime, decodedLock.Until)
}

// TestLockUnlocker verifies the LockUnlocker functionality
func TestLockUnlocker(t *testing.T) {
	// Create a test private key
	privKey, err := ec.NewPrivateKey()
	require.NoError(t, err)

	// Get the public key hash
	pubKey := privKey.PubKey()
	pubKeyBytes := pubKey.Compressed()

	// Create address from public key hash
	address, err := script.NewAddressFromPublicKeyHash(pubKeyBytes[:20], true)
	require.NoError(t, err)

	// Create a lock for 1 hour in the future
	lockTime := uint32(time.Now().Unix()) + 3600
	lock := &Lock{
		Address: address,
		Until:   lockTime,
	}

	// Create the locking script
	lockScript := lock.Lock()
	require.NotNil(t, lockScript)

	// Create a transaction
	tx := transaction.NewTransaction()
	tx.Version = 1
	tx.LockTime = 0

	// Convert locking script to hex for AddInputFrom
	lockScriptHex := hex.EncodeToString(*lockScript)

	// Add an input using the lockup script
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
	p2pkhBytes = append(p2pkhBytes, address.PublicKeyHash...)
	p2pkhBytes = append(p2pkhBytes, script.OpEQUALVERIFY, script.OpCHECKSIG)
	p2pkhScript := script.Script(p2pkhBytes)

	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      99999000, // Minus fees
		LockingScript: &p2pkhScript,
	})

	// Create a lockup unlocker
	shf := sighash.AllForkID
	unlocker := LockUnlocker{
		PrivateKey:  privKey,
		SigHashFlag: &shf,
	}

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

// TestLockScriptFormat verifies the format of a lock script
func TestLockScriptFormat(t *testing.T) {
	// Generate a test address
	privKey, err := ec.NewPrivateKey()
	require.NoError(t, err)
	address, err := script.NewAddressFromPublicKey(privKey.PubKey(), true)
	require.NoError(t, err)

	// Create a lock for 1 hour in the future
	lockTime := uint32(time.Now().Unix()) + 3600
	lock := &Lock{
		Address: address,
		Until:   lockTime,
	}

	// Create the locking script
	lockScript := lock.Lock()
	require.NotNil(t, lockScript)

	// Verify the script is properly formed
	chunks, err := lockScript.Chunks()
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	// Verify the script format - check prefix at the beginning and suffix at the end
	scriptBytes := *lockScript
	require.True(t, bytes.HasPrefix(scriptBytes, LockPrefix), "Script should start with LockPrefix")
	require.True(t, bytes.HasSuffix(scriptBytes, LockSuffix), "Script should end with LockSuffix")

	// Extract the public key hash from the script
	// The PKH should be in the first PUSHDATA after the prefix
	pos := len(LockPrefix)
	op, err := lockScript.ReadOp(&pos)
	require.NoError(t, err)

	// Verify the extracted PKH matches the address PKH
	require.Equal(t, []byte(address.PublicKeyHash), op.Data, "Address public key hash mismatch in script")

	// Verify the locktime is encoded in the script
	untilBytes := (&interpreter.ScriptNumber{
		Val:          big.NewInt(int64(lockTime)),
		AfterGenesis: true,
	}).Bytes()

	// Check if the script contains the locktime bytes
	op, err = lockScript.ReadOp(&pos)
	require.NoError(t, err)
	require.Equal(t, untilBytes, op.Data, "Locktime bytes mismatch in script")
}

// TestLockDecodeInvalid verifies that Decode properly handles invalid scripts
func TestLockDecodeInvalid(t *testing.T) {
	// Create an invalid script
	invalidScript := script.NewFromBytes([]byte{})
	_ = invalidScript.AppendOpcodes(script.OpRETURN)

	// Try to decode
	decodedLock := Decode(invalidScript, true)
	require.Nil(t, decodedLock)

	// Create a script with valid prefix but invalid content
	invalidWithPrefix := script.NewFromBytes(LockPrefix)
	_ = invalidWithPrefix.AppendOpcodes(script.OpRETURN)

	// Try to decode
	decodedLock = Decode(invalidWithPrefix, true)
	require.Nil(t, decodedLock)
}

// TestLockDecodeWithInvalidPKH verifies that Decode properly handles invalid public key hash
func TestLockDecodeWithInvalidPKH(t *testing.T) {
	// Create a script with valid prefix but invalid PKH length
	invalidPKHScript := script.NewFromBytes(LockPrefix)
	_ = invalidPKHScript.AppendPushData([]byte("invalidpkh")) // Not 20 bytes
	_ = invalidPKHScript.AppendPushData([]byte{0, 0, 0, 0})   // Valid locktime bytes
	invalidPKHScript = script.NewFromBytes(append(*invalidPKHScript, LockSuffix...))

	// Try to decode
	decodedLock := Decode(invalidPKHScript, true)
	require.Nil(t, decodedLock)
}
