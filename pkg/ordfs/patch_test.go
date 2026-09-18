package ordfs

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/go-deltasync/vcdiff"
)

func TestParsePatch(t *testing.T) {
	txid := bytes.Repeat([]byte{0x11}, 32)
	var op [36]byte
	copy(op[:32], txid)
	binary.LittleEndian.PutUint32(op[32:], 7)
	delta := vcdiff.EncodeBytes([]byte("old"), []byte("new"))
	raw := append([]byte{patchVersion}, op[:]...)
	raw = append(raw, delta...)

	rec, err := parsePatch(raw)
	if err != nil {
		t.Fatal(err)
	}
	h, err := chainhash.NewHash(txid)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Base.Txid.Equal(*h) || rec.Base.Index != 7 {
		t.Fatalf("base = %s_%d", rec.Base.Txid, rec.Base.Index)
	}
	got, err := applyVCDIFF([]byte("old"), rec.Delta)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestParsePatchErrors(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
	}{
		{"empty", nil},
		{"short", bytes.Repeat([]byte{0x00}, 20)},
		{"bad version", append([]byte{0x01}, bytes.Repeat([]byte{0x00}, 36)...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parsePatch(tt.b); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestApplyVCDIFF(t *testing.T) {
	src := []byte("hello world")
	dst := []byte("hello there")
	delta := vcdiff.EncodeBytes(src, dst)
	got, err := applyVCDIFF(src, delta)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("got %q want %q", got, dst)
	}
	if _, err := applyVCDIFF(src, []byte("not a delta")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParsePatchOutpoint(t *testing.T) {
	op := &transaction.Outpoint{}
	copy(op.Txid[:], bytes.Repeat([]byte{0x22}, 32))
	op.Index = 1
	raw := append([]byte{patchVersion}, op.Bytes()...)
	rec, err := parsePatch(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Base.Index != 1 {
		t.Fatalf("index %d", rec.Base.Index)
	}
}
