package parse

import (
	"encoding/hex"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// v2 listing vector: seller PKH 0x11 x20, payout 1000 sats to P2PKH 0x22 x20
// (same vector as pkg/template/ordlock and the ordlock-v2 harness).
const v2ListingHex = "76009c637576ab76aa517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e01007e8100011f80517e9321414136d08c5ed2bf3ba048afe6dcaebafeffffffffffffffffffffffffffffff007d97785296789f527952798d9495937776927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e827c7e23022079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798027c7e827c7e01307c7e01c37e2102b405d7f0322a89d0f9f3a98e6f938fdc1c969a8d1382a2bf66a71ae74a1e83b0ad6922e8030000000000001976a914222222222222222222222222222222222222222288acaa7c820128947f7701207f758767519d7b0a6f6c323a63616e63656c8876a914111111111111111111111111111111111111111188ac68"

func v1ListingScript(t *testing.T) []byte {
	t.Helper()
	pkh := make([]byte, 20)
	for i := range pkh {
		pkh[i] = 0x11
	}
	payScript := &script.Script{}
	_ = payScript.AppendOpcodes(script.OpDUP, script.OpHASH160)
	_ = payScript.AppendPushData(pkh)
	_ = payScript.AppendOpcodes(script.OpEQUALVERIFY, script.OpCHECKSIG)
	payout := (&transaction.TransactionOutput{Satoshis: 1000, LockingScript: payScript}).Bytes()
	s := &script.Script{}
	*s = append(*s, ordlock.OrdLockPrefix...)
	_ = s.AppendPushData(pkh)
	_ = s.AppendPushData(payout)
	*s = append(*s, ordlock.OrdLockSuffix...)
	return *s
}

func parseListing(t *testing.T, lockingScript []byte, sats uint64) *ParseResult {
	t.Helper()
	ctx := &ParseContext{
		Outpoint:      &transaction.Outpoint{},
		LockingScript: lockingScript,
		Satoshis:      sats,
		Results:       map[string]*ParseResult{},
	}
	res, err := ParseOrdLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestParseOrdLockV2EmitsEventAndOwner(t *testing.T) {
	lock, err := hex.DecodeString(v2ListingHex)
	if err != nil {
		t.Fatal(err)
	}
	res := parseListing(t, lock, 1)
	if res == nil || res.Tag != TagOrdLockV2 {
		t.Fatalf("v2 listing must parse as %s, got %+v", TagOrdLockV2, res)
	}
	if len(res.Events) != 1 || res.Events[0] != TagOrdLockV2 {
		t.Fatalf("v2 must emit the %s event, got %v", TagOrdLockV2, res.Events)
	}
	if len(res.Owners) != 1 || hex.EncodeToString((*res.Owners[0])[:]) != "1111111111111111111111111111111111111111" {
		t.Fatalf("v2 must index the seller PKH as owner, got %v", res.Owners)
	}
	ol, ok := res.Data.(*ordlock.OrdLock)
	if !ok || ol.Price != 1000 {
		t.Fatalf("v2 data must be the decoded listing (price 1000), got %+v", res.Data)
	}

	// Trailing data (e.g. MAP appended by the SDK) must not break recognition.
	trailer := &script.Script{}
	_ = trailer.AppendOpcodes(script.OpRETURN)
	_ = trailer.AppendPushData([]byte("SET"))
	withMap := append(append([]byte{}, lock...), *trailer...)
	if r := parseListing(t, withMap, 1); r == nil || r.Tag != TagOrdLockV2 {
		t.Fatalf("v2 listing with MAP trailer must still parse, got %+v", r)
	}

	// Not a listing unless it carries exactly 1 sat.
	if r := parseListing(t, lock, 2); r != nil {
		t.Fatalf("2-sat output must not parse as a listing, got %+v", r)
	}
}

func TestParseOrdLockV1RetainsDataWithoutEvent(t *testing.T) {
	res := parseListing(t, v1ListingScript(t), 1)
	if res == nil || res.Tag != TagOrdLock {
		t.Fatalf("v1 listing must parse as %s, got %+v", TagOrdLock, res)
	}
	if len(res.Events) != 0 {
		t.Fatalf("deprecated v1 must not emit a public event, got %v", res.Events)
	}
	if len(res.Owners) != 1 {
		t.Fatalf("v1 must still index the cancel address for sweep recovery, got %v", res.Owners)
	}
}

func TestParseOrdLockIgnoresOtherScripts(t *testing.T) {
	p2pkh, _ := hex.DecodeString("76a914" + "1111111111111111111111111111111111111111" + "88ac")
	if r := parseListing(t, p2pkh, 1); r != nil {
		t.Fatalf("P2PKH must not parse as a listing, got %+v", r)
	}
}
