package ordfs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestParseDir(t *testing.T) {
	txid := bytes.Repeat([]byte{0xab}, 32)
	var opBuf [36]byte
	copy(opBuf[:32], txid)
	binary.LittleEndian.PutUint32(opBuf[32:], 3)

	valid := concatBytes(
		[]byte{dirVersion, 0x00, 0x04},
		dirEntry(0x08, "LICENSE", opBuf[:]),
		dirEntry(0x00, "README.md", []byte{0x01}),
		dirEntry(0x00, "package.json", []byte{0x06}),
		dirEntry(0x01, "src", []byte{0x0b}),
	)

	got, err := parseDir(valid)
	if err != nil {
		t.Fatalf("parseDir: %v", err)
	}
	wantLicense := transaction.NewOutpointFromBytes(opBuf[:])
	want := fmt.Sprintf("%s_%d", wantLicense.Txid.String(), wantLicense.Index)
	if got["LICENSE"] != want {
		t.Errorf("LICENSE = %q, want %q", got["LICENSE"], want)
	}
	if got["README.md"] != "_1" {
		t.Errorf("README.md = %q, want _1", got["README.md"])
	}
	if got["package.json"] != "_6" {
		t.Errorf("package.json = %q, want _6", got["package.json"])
	}
	if got["src"] != "_11" {
		t.Errorf("src = %q, want _11", got["src"])
	}
}

func TestParseDirErrors(t *testing.T) {
	txid := bytes.Repeat([]byte{0xcd}, 32)
	var opBuf [36]byte
	copy(opBuf[:32], txid)

	tests := []struct {
		name string
		b    []byte
	}{
		{"empty", nil},
		{"short", []byte{dirVersion, 0x00}},
		{"bad version", []byte{0x02, 0x00, 0x00}},
		{"truncated entry", []byte{dirVersion, 0x00, 0x01, 0x00}},
		{"zero name", concatBytes([]byte{dirVersion, 0x00, 0x01}, []byte{0x00, 0x00})},
		{"slash in name", concatBytes([]byte{dirVersion, 0x00, 0x01}, dirEntry(0x00, "a/b", []byte{0x01}))},
		{"nul in name", concatBytes([]byte{dirVersion, 0x00, 0x01}, dirEntry(0x00, "a\x00b", []byte{0x01}))},
		{"reserved bits", concatBytes([]byte{dirVersion, 0x00, 0x01}, dirEntry(0x10, "a", []byte{0x01}))},
		{"unsorted", concatBytes(
			[]byte{dirVersion, 0x00, 0x02},
			dirEntry(0x00, "b", []byte{0x01}),
			dirEntry(0x00, "a", []byte{0x02}),
		)},
		{"duplicate", concatBytes(
			[]byte{dirVersion, 0x00, 0x02},
			dirEntry(0x00, "a", []byte{0x01}),
			dirEntry(0x00, "a", []byte{0x02}),
		)},
		{"trailing", concatBytes([]byte{dirVersion, 0x00, 0x00}, []byte{0xff})},
		{"truncated outpoint", concatBytes(
			[]byte{dirVersion, 0x00, 0x01},
			[]byte{dirFlagReftype, 0x01, 'a'},
			bytes.Repeat([]byte{0x01}, 10),
		)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseDir(tt.b); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseDirectoryJSON(t *testing.T) {
	got, err := parseDirectory(contentTypeJSONDir, []byte(`{"index.html":"_1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got["index.html"] != "_1" {
		t.Fatalf("got %#v", got)
	}
	if _, err := parseDirectory(contentTypeJSONDir, []byte(`[`)); err == nil {
		t.Fatal("expected error")
	}
}

func dirEntry(flags byte, name string, target []byte) []byte {
	b := []byte{flags, byte(len(name))}
	b = append(b, name...)
	return append(b, target...)
}

func concatBytes(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
