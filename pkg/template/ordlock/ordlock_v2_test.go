package ordlock

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

// Canonical v2 listing: seller PKH 0x11.., payout P2PKH 0x22.. for 1000 sats.
// Regenerate via ordlock-v2/harness TestGenerateV2Vector.
const v2ListingHex = "76009c637576ab76aa517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e01007e8100011f80517e9321414136d08c5ed2bf3ba048afe6dcaebafeffffffffffffffffffffffffffffff007d97785296789f527952798d9495937776927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f76927f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e827c7e23022079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798027c7e827c7e01307c7e01c17e2102b405d7f0322a89d0f9f3a98e6f938fdc1c969a8d1382a2bf66a71ae74a1e83b0ad690c000000000000000027006a247801447f7701247f757e537a22e8030000000000001976a914222222222222222222222222222222222222222288ac7e7c7e7b7eaa7c820128947f7701207f758767519d7b0a6f6c323a63616e63656c8876a914111111111111111111111111111111111111111188ac68"

func TestDecodeV2(t *testing.T) {
	scr, err := script.NewFromHex(v2ListingHex)
	require.NoError(t, err)

	require.True(t, IsOrdLockV2(scr))
	ol := DecodeV2(scr)
	require.NotNil(t, ol, "v2 listing must decode")

	// seller = 0x11 * 20
	want := make([]byte, 20)
	for i := range want {
		want[i] = 0x11
	}
	require.Equal(t, want, []byte(ol.Seller.PublicKeyHash))
	require.Equal(t, uint64(1000), ol.Price)

	// payout must be a standard P2PKH to 0x22.. (the property that keeps
	// address-based discovery working)
	payout := &transaction.TransactionOutput{}
	_, err = payout.ReadFrom(sliceReader(ol.PayOut))
	require.NoError(t, err)
	require.Equal(t, uint64(1000), payout.Satoshis)
	require.Equal(t, 25, len(*payout.LockingScript), "payout is a bare 25-byte P2PKH")
}

func TestDecodeV2RejectsV1AndP2PKH(t *testing.T) {
	// a bare P2PKH is not a v2 listing
	p2pkh, _ := script.NewFromHex("76a914" + "0000000000000000000000000000000000000000" + "88ac")
	require.False(t, IsOrdLockV2(p2pkh))
	require.Nil(t, DecodeV2(p2pkh))

	// v1 ordlock prefix is not v2
	v1 := script.NewFromBytes(OrdLockPrefix)
	require.False(t, IsOrdLockV2(v1))
}

func sliceReader(b []byte) *byteReader { return &byteReader{b: b} }

type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, errEOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

var errEOF = &eofErr{}

type eofErr struct{}

func (e *eofErr) Error() string { return "EOF" }
