package collection

import (
	"fmt"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/wallet"
)

// FeeIdentityPubKeyHex is the compressed secp256k1 public key collection
// payments are derived from.
//
// Derivation is BRC-42 against the anyone counterparty. The invoice number is
// the collection outpoint in ordinal form (txid_vout). The operator spends
// with the matching private key derived against the anyone public key and the
// same invoice.
const FeeIdentityPubKeyHex = "02c96292e1fc788be7de3f7bf6bfcbb7b61dc6bda215573c9d3b38b4d229867761"

// FeePerOutput is the satoshis charged for each admitted collection output.
// Matches the BSV-21 default until a collection-specific rate is chosen.
const FeePerOutput int64 = 1000

// FeeAddress returns the mainnet P2PKH address that pays for a collection.
func FeeAddress(collection *transaction.Outpoint) (string, error) {
	if collection == nil {
		return "", fmt.Errorf("collection outpoint is required")
	}
	if FeeIdentityPubKeyHex == "" {
		return "", fmt.Errorf("collection fee identity public key is not set")
	}
	return deriveFeeAddress(FeeIdentityPubKeyHex, collection.OrdinalString())
}

func deriveFeeAddress(identityPubKeyHex, invoice string) (string, error) {
	identity, err := ec.PublicKeyFromString(identityPubKeyHex)
	if err != nil {
		return "", fmt.Errorf("parse fee identity public key: %w", err)
	}
	anyonePriv, _ := wallet.AnyoneKey()
	derived, err := identity.DeriveChild(anyonePriv, invoice)
	if err != nil {
		return "", fmt.Errorf("derive collection fee address: %w", err)
	}
	address, err := script.NewAddressFromPublicKey(derived, true)
	if err != nil {
		return "", fmt.Errorf("encode collection fee address: %w", err)
	}
	return address.AddressString, nil
}
