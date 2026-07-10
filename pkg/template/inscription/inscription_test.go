package inscription

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
)

func TestDecode_InvalidScript(t *testing.T) {
	s := script.NewFromBytes([]byte{0x00, 0x51, 0x52}) // random bytes, not a valid inscription
	insc := Decode(s)
	if insc != nil {
		t.Errorf("expected nil for invalid script, got %+v", insc)
	}
}

func TestLock_Basic(t *testing.T) {
	insc := &Inscription{
		File: File{
			Type:    "text/plain",
			Content: []byte("hello world"),
		},
	}
	script, err := insc.Lock()
	if err != nil {
		t.Fatalf("Lock error: %v", err)
	}
	if script == nil || len(*script) == 0 {
		t.Error("expected non-empty script")
	}
}

func TestRoundTrip_LockDecode(t *testing.T) {
	insc := &Inscription{
		File: File{
			Type:    "text/plain",
			Content: []byte("round trip test content"),
		},
	}
	script, err := insc.Lock()
	if err != nil {
		t.Fatalf("Lock error: %v", err)
	}
	decoded := Decode(script)
	if decoded == nil {
		t.Fatalf("Decode failed, got nil")
	}
	if decoded.File.Type != insc.File.Type {
		t.Errorf("File.Type mismatch: got %q, want %q", decoded.File.Type, insc.File.Type)
	}
	if string(decoded.File.Content) != string(insc.File.Content) {
		t.Errorf("File.Content mismatch: got %q, want %q", string(decoded.File.Content), string(insc.File.Content))
	}
}
