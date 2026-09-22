package collection

import (
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/wallet"
)

func TestFeeAddressRequiresIdentityKey(t *testing.T) {
	txid, _ := chainhash.NewHashFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	_, err := FeeAddress(&transaction.Outpoint{Txid: *txid, Index: 0})
	if FeeIdentityPubKeyHex == "" {
		if err == nil || !strings.Contains(err.Error(), "not set") {
			t.Fatalf("empty identity key: got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeriveFeeAddressMatchesSpendKey(t *testing.T) {
	identity, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	txid, _ := chainhash.NewHashFromHex("2222222222222222222222222222222222222222222222222222222222222222")
	invoice := (&transaction.Outpoint{Txid: *txid, Index: 3}).OrdinalString()

	got, err := deriveFeeAddress(identity.PubKey().ToDERHex(), invoice)
	if err != nil {
		t.Fatal(err)
	}

	_, anyonePub := wallet.AnyoneKey()
	spendKey, err := identity.DeriveChild(anyonePub, invoice)
	if err != nil {
		t.Fatal(err)
	}
	want, err := script.NewAddressFromPublicKey(spendKey.PubKey(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got != want.AddressString {
		t.Fatalf("fee address %s does not match spend key %s", got, want.AddressString)
	}
	if !strings.HasPrefix(got, "1") {
		t.Fatalf("expected mainnet address, got %s", got)
	}
}
