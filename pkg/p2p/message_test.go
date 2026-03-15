package p2p

import (
	"crypto/rand"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
)

func randomHash(t *testing.T) chainhash.Hash {
	t.Helper()
	var h chainhash.Hash
	if _, err := rand.Read(h[:]); err != nil {
		t.Fatal(err)
	}
	return h
}

func randomHashPtr(t *testing.T) *chainhash.Hash {
	t.Helper()
	h := randomHash(t)
	return &h
}

func TestRoundTrip_Empty(t *testing.T) {
	msg := &SteakMessage{
		Txid: randomHash(t),
	}

	data := msg.Serialize()
	got, err := DeserializeSteakMessage(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Version != SteakVersion2 {
		t.Errorf("version: want %d, got %d", SteakVersion2, got.Version)
	}
	if got.Txid != msg.Txid {
		t.Errorf("txid mismatch")
	}
	if len(got.Instructions.OutputsToAdmit) != 0 {
		t.Errorf("expected empty OutputsToAdmit")
	}
	if got.TxSize != 0 {
		t.Errorf("expected zero txSize")
	}
	if got.PriceSats != 0 {
		t.Errorf("expected zero priceSats")
	}

	// 1 version + 32 txid + 4 varint zeros + 4 txSize + 8 priceSats = 49
	if len(data) != 49 {
		t.Errorf("expected 49 bytes for empty message, got %d", len(data))
	}
}

func TestRoundTrip_Populated(t *testing.T) {
	msg := &SteakMessage{
		Txid: randomHash(t),
		Instructions: overlay.AdmittanceInstructions{
			OutputsToAdmit: []uint32{0, 1, 2},
			CoinsToRetain:  []uint32{0},
			CoinsRemoved:   []uint32{1, 3},
			AncillaryTxids: []*chainhash.Hash{
				randomHashPtr(t),
				randomHashPtr(t),
			},
		},
		TxSize:    4280,
		PriceSats: 50,
	}

	data := msg.Serialize()
	got, err := DeserializeSteakMessage(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Txid != msg.Txid {
		t.Errorf("txid mismatch")
	}

	assertUint32Slice(t, "OutputsToAdmit", msg.Instructions.OutputsToAdmit, got.Instructions.OutputsToAdmit)
	assertUint32Slice(t, "CoinsToRetain", msg.Instructions.CoinsToRetain, got.Instructions.CoinsToRetain)
	assertUint32Slice(t, "CoinsRemoved", msg.Instructions.CoinsRemoved, got.Instructions.CoinsRemoved)

	if len(got.Instructions.AncillaryTxids) != 2 {
		t.Fatalf("expected 2 ancillary txids, got %d", len(got.Instructions.AncillaryTxids))
	}
	for i, h := range got.Instructions.AncillaryTxids {
		if *h != *msg.Instructions.AncillaryTxids[i] {
			t.Errorf("ancillary txid %d mismatch", i)
		}
	}

	if got.TxSize != 4280 {
		t.Errorf("txSize: want 4280, got %d", got.TxSize)
	}
	if got.PriceSats != 50 {
		t.Errorf("priceSats: want 50, got %d", got.PriceSats)
	}
}

func TestRoundTrip_LargeLists(t *testing.T) {
	outputs := make([]uint32, 256)
	for i := range outputs {
		outputs[i] = uint32(i)
	}

	ancillary := make([]*chainhash.Hash, 10)
	for i := range ancillary {
		ancillary[i] = randomHashPtr(t)
	}

	msg := &SteakMessage{
		Txid: randomHash(t),
		Instructions: overlay.AdmittanceInstructions{
			OutputsToAdmit: outputs,
			AncillaryTxids: ancillary,
		},
		TxSize:    1_000_000,
		PriceSats: 500,
	}

	data := msg.Serialize()
	got, err := DeserializeSteakMessage(data)
	if err != nil {
		t.Fatal(err)
	}

	assertUint32Slice(t, "OutputsToAdmit", msg.Instructions.OutputsToAdmit, got.Instructions.OutputsToAdmit)
	if len(got.Instructions.AncillaryTxids) != 10 {
		t.Fatalf("expected 10 ancillary txids, got %d", len(got.Instructions.AncillaryTxids))
	}
	if got.TxSize != 1_000_000 {
		t.Errorf("txSize: want 1000000, got %d", got.TxSize)
	}
}

func TestRoundTrip_FreeTx(t *testing.T) {
	msg := &SteakMessage{
		Txid: randomHash(t),
		Instructions: overlay.AdmittanceInstructions{
			OutputsToAdmit: []uint32{0},
		},
		TxSize:    250,
		PriceSats: 0, // free
	}

	data := msg.Serialize()
	got, err := DeserializeSteakMessage(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.PriceSats != 0 {
		t.Errorf("expected free (0 sats), got %d", got.PriceSats)
	}
	if got.TxSize != 250 {
		t.Errorf("txSize: want 250, got %d", got.TxSize)
	}
}

func TestDeserialize_TooShort(t *testing.T) {
	_, err := DeserializeSteakMessage([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for short input")
	}
}

func TestDeserialize_UnsupportedVersion(t *testing.T) {
	// Build a message then change the version byte
	msg := &SteakMessage{Txid: randomHash(t)}
	data := msg.Serialize()
	data[0] = 99 // unsupported version
	_, err := DeserializeSteakMessage(data)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestDeserialize_TrailingBytes(t *testing.T) {
	msg := &SteakMessage{Txid: randomHash(t)}
	data := append(msg.Serialize(), 0xFF)
	_, err := DeserializeSteakMessage(data)
	if err == nil {
		t.Fatal("expected error for trailing bytes")
	}
}

func TestDeserialize_TruncatedList(t *testing.T) {
	msg := &SteakMessage{
		Txid: randomHash(t),
		Instructions: overlay.AdmittanceInstructions{
			OutputsToAdmit: []uint32{0, 1, 2},
		},
	}
	data := msg.Serialize()
	// Truncate in the middle of the uint32 list
	_, err := DeserializeSteakMessage(data[:36])
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func assertUint32Slice(t *testing.T, name string, want, got []uint32) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: length mismatch: want %d, got %d", name, len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("%s[%d]: want %d, got %d", name, i, want[i], got[i])
		}
	}
}
