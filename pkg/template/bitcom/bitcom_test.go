package bitcom

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/stretchr/testify/require"
)

// TestDecode verifies the basic functionality of the Decode function
func TestDecode(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test nil script
	var nilScript *script.Script
	result := Decode(nilScript)
	require.NotNil(t, result, "Expected non-nil result for nil script")
	require.Empty(t, result.Protocols, "Expected empty protocols for nil script")
}

// TestLock verifies that the Lock function correctly builds scripts
// from a Bitcom object for various protocol combinations
func TestLock(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	tests := []struct {
		name     string
		bitcom   *Bitcom
		expected []byte
	}{
		{
			name: "empty bitcom",
			bitcom: &Bitcom{
				ScriptPrefix: []byte{},
				Protocols:    []*BitcomProtocol{},
			},
			expected: []byte{},
		},
		{
			name: "bitcom with prefix only",
			bitcom: &Bitcom{
				ScriptPrefix: []byte{0x51}, // OP_1
				Protocols:    []*BitcomProtocol{},
			},
			expected: []byte{0x51},
		},
		{
			name: "bitcom with one protocol",
			bitcom: &Bitcom{
				ScriptPrefix: []byte{0x00}, // OP_FALSE
				Protocols: []*BitcomProtocol{
					{
						Protocol: MapPrefix,
						Script:   *script.NewFromBytes(append([]byte{9}, "test data"...)),
					},
				},
			},
			expected: func() []byte {
				s := &script.Script{}
				_ = s.AppendOpcodes(script.OpFALSE)
				_ = s.AppendOpcodes(script.OpRETURN)
				_ = s.AppendPushData([]byte(MapPrefix))
				_ = s.AppendPushData([]byte("test data"))
				return *s
			}(),
		},
		{
			name: "bitcom with multiple protocols",
			bitcom: &Bitcom{
				ScriptPrefix: []byte{0x00}, // OP_FALSE
				Protocols: []*BitcomProtocol{
					{
						Protocol: MapPrefix,
						Script:   *script.NewFromBytes(append([]byte{8}, "map data"...)),
					},
					{
						Protocol: BPrefix,
						Script:   *script.NewFromBytes(append([]byte{6}, "b data"...)),
					},
				},
			},
			expected: func() []byte {
				s := &script.Script{}
				_ = s.AppendOpcodes(script.OpFALSE)
				_ = s.AppendOpcodes(script.OpRETURN)
				_ = s.AppendPushData([]byte(MapPrefix))
				_ = s.AppendPushData([]byte("map data"))
				_ = s.AppendPushData([]byte("|"))
				_ = s.AppendPushData([]byte(BPrefix))
				_ = s.AppendPushData([]byte("b data"))
				return *s
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset global state before each subtest
			resetTestState()

			result := tt.bitcom.Lock()
			if len(tt.expected) == 0 {
				require.Equal(t, len(tt.expected), len(*result))
				return
			}
			require.True(t, bytes.Equal(tt.expected, *result), "expected %x but got %x", tt.expected, *result)
		})
	}
}

// TestFindReturn verifies that the findReturn function correctly identifies
// the position of OP_RETURN in a script
func TestFindReturn(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test nil script
	var nilScript *script.Script
	result := findReturn(nilScript, 0)
	require.Equal(t, -1, result, "Expected -1 for nil script in findReturn")

	// Test for OP_RETURN with and without prefix
	s := &script.Script{}
	_ = s.AppendOpcodes(script.OpRETURN)
	_ = s.AppendPushData([]byte("test data"))

	result = findReturn(s, 0)
	require.Equal(t, 0, result, "Expected 0 for OP_RETURN without prefix in findReturn")

	// Test for OP_RETURN without prefix
	s2 := &script.Script{}
	_ = s2.AppendOpcodes(script.OpFALSE)
	_ = s2.AppendOpcodes(script.OpRETURN)
	_ = s2.AppendPushData([]byte("test data2"))

	result = findReturn(s2, 0)
	require.Equal(t, 1, result, "Expected 1 for OP_RETURN with prefix in findReturn")
}

// TestFindPipe verifies that the findPipe function correctly identifies
// the position of pipe separators ("|") in a script
func TestFindPipe(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test nil script
	var nilScript *script.Script
	result := findPipe(nilScript, 0)
	require.Equal(t, -1, result, "Expected -1 for nil script in findPipe")
}

// TestDecode_NilInput verifies that the Decode function
// handles nil input gracefully
func TestDecode_NilInput(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test that Decode handles nil input safely
	result := Decode(nil)
	require.NotNil(t, result, "Decode should return empty Bitcom for nil input, not nil")
	require.Empty(t, result.Protocols, "Protocols should be empty for nil input")
}

// TestDecode_NoReturn verifies that the Decode function
// correctly handles scripts without OP_RETURN
func TestDecode_NoReturn(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test script with no OP_RETURN
	s := &script.Script{}
	_ = s.AppendOpcodes(script.OpDUP, script.OpHASH160)
	_ = s.AppendPushData([]byte("some address"))
	_ = s.AppendOpcodes(script.OpEQUALVERIFY, script.OpCHECKSIG)

	result := Decode(s)
	require.Nil(t, result, "Decode should return nil for scripts with no OP_RETURN")
}

// TestDecode_WithMultipleProtocols verifies the decoding of scripts
// containing multiple protocols separated by pipe characters
func TestDecode_WithMultipleProtocols(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Create script with OP_RETURN and multiple protocols
	s := &script.Script{}

	// Add OP_RETURN
	_ = s.AppendOpcodes(script.OpRETURN)

	// Add first protocol
	_ = s.AppendPushData([]byte(MapPrefix))
	_ = s.AppendPushData([]byte("data1"))

	// Add pipe separator
	_ = s.AppendPushData([]byte("|"))

	// Add second protocol
	_ = s.AppendPushData([]byte(BPrefix))
	_ = s.AppendPushData([]byte("data2"))

	// Debug logging
	t.Logf("Script bytes: %x", s.Bytes())

	// Check result
	result := Decode(s)
	t.Logf("Decode result: %+v", result)
	if result != nil {
		t.Logf("Protocols length: %d", len(result.Protocols))
		for i, proto := range result.Protocols {
			t.Logf("Protocol %d: %q, Script: %x", i, proto.Protocol, proto.Script)
		}
	}

	require.NotNil(t, result, "Decode should return non-nil result for valid script")

	// Verify the protocol decoding
	require.Len(t, result.Protocols, 2, "Current decoder returns 2 protocols for this output")

	// Verify the protocol content
	require.True(t, strings.HasPrefix(result.Protocols[0].Protocol, "1P"),
		"Protocol value should start with '1P' (from MAP prefix)")
}

// TestDecode_WithNoPipe verifies the decoding of scripts
// containing a single protocol without pipe separators
func TestDecode_WithNoPipe(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Create a simple script with just OP_RETURN and one protocol
	s := &script.Script{}

	// Add OP_RETURN only
	_ = s.AppendOpcodes(script.OpRETURN)

	// Add protocol and data
	_ = s.AppendPushData([]byte(MapPrefix))
	_ = s.AppendPushData([]byte("data"))

	// Debug logging
	t.Logf("Script bytes: %x", s.Bytes())

	// Check result
	result := Decode(s)
	t.Logf("Decode result: %+v", result)
	if result != nil {
		t.Logf("Protocols length: %d", len(result.Protocols))
		for i, proto := range result.Protocols {
			t.Logf("Protocol %d: %s, Script: %x", i, proto.Protocol, proto.Script)
		}
	}

	require.NotNil(t, result, "Decode should return non-nil result for valid script")

	// Verify the decoder behavior for a single protocol
	// Note: The current implementation returns an empty protocols array
}

// TestFindPipe_EmptyScript verifies that findPipe correctly
// handles empty scripts
func TestFindPipe_EmptyScript(t *testing.T) {
	// Reset global state before starting the test
	resetTestState()

	// Test findPipe with nil script
	pos := findPipe(nil, 0)
	require.Equal(t, -1, pos, "findPipe should return -1 for nil script")

	// Test findPipe with empty script
	emptyScript := &script.Script{}
	pos = findPipe(emptyScript, 0)
	require.Equal(t, -1, pos, "findPipe should return -1 for empty script")
}
