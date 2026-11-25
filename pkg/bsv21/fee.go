package bsv21

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/b-open-io/1sat-stack/pkg/types"
	bip32 "github.com/bsv-blockchain/go-sdk/compat/bip32"
	bsvhash "github.com/bsv-blockchain/go-sdk/primitives/hash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/util"
)

// HD key for fee address generation (matches 1sat-indexer and bsv21-overlay-1sat-sync)
const hdKeyString = "xpub661MyMwAqRbcF221R74MPqdipLsgUevAAX4hZP2rywyEeShpbe3v2r9ciAvSGT6FB22TEmFLdUyeEDJL4ekG8s9H5WXbzDQPr6eW1zEYYy9"

var hdKey *bip32.ExtendedKey

func init() {
	var err error
	hdKey, err = bip32.GetHDKeyFromExtendedPublicKey(hdKeyString)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize HD key: %v", err))
	}
}

// GenerateFeeAddress generates a deterministic Bitcoin address for a token's fee payments.
// The address is derived from the token outpoint using a deterministic HD key derivation path.
// This matches the 1sat-indexer v4 format: big-endian txid hex bytes + big-endian vout.
func GenerateFeeAddress(outpoint *transaction.Outpoint, network ...types.Network) (string, error) {
	// Build outpoint bytes in v4 format: txid as hex-order bytes (reversed from chainhash) + big-endian vout
	outpointBytes := binary.BigEndian.AppendUint32(util.ReverseBytes(outpoint.Txid[:]), outpoint.Index)
	hash := sha256.Sum256(outpointBytes)

	path := fmt.Sprintf("21/%d/%d",
		binary.BigEndian.Uint32(hash[:8])>>1,
		binary.BigEndian.Uint32(hash[24:])>>1)

	ek, err := hdKey.DeriveChildFromPath(path)
	if err != nil {
		return "", fmt.Errorf("failed to derive key for path %s: %w", path, err)
	}

	pubKey, err := ek.ECPubKey()
	if err != nil {
		return "", fmt.Errorf("failed to get public key: %w", err)
	}

	pubKeyBytes := pubKey.Compressed()
	pkHash := bsvhash.Hash160(pubKeyBytes)

	mainnet := true
	if len(network) > 0 {
		mainnet = network[0].Mainnet()
	}
	address, err := script.NewAddressFromPublicKeyHash(pkHash, mainnet)
	if err != nil {
		return "", fmt.Errorf("failed to create address: %w", err)
	}

	return address.AddressString, nil
}
