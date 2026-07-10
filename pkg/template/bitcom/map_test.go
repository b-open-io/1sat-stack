package bitcom

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/stretchr/testify/require"
)

// resetTestState resets any global state that affects test outcomes.
// Call this function at the beginning of each test function and subtest
// to ensure tests don't interfere with each other through shared state.
func resetTestState() {
	// Reset the global script position counter
	ZERO = 0
}

func TestDecodeMap(t *testing.T) {
	// Reset test state before each test
	resetTestState()

	t.Run("empty script", func(t *testing.T) {
		// Reset test state before each subtest
		resetTestState()

		emptyScript := script.Script{}
		result := DecodeMap(emptyScript)
		require.Nil(t, result, "Expected nil result for empty script")
	})

	t.Run("SET command with bsocial post data", func(t *testing.T) {
		// Reset test state before each subtest
		resetTestState()

		s := &script.Script{}

		t.Logf("Adding MapCmdSet: %s", MapCmdSet)
		_ = s.AppendPushData([]byte(MapCmdSet))
		t.Logf("Adding key 'app'")
		_ = s.AppendPushData([]byte("app"))
		t.Logf("Adding value 'bsocial'")
		_ = s.AppendPushData([]byte("bsocial"))
		t.Logf("Adding key 'type'")
		_ = s.AppendPushData([]byte("type"))
		t.Logf("Adding value 'post'")
		_ = s.AppendPushData([]byte("post"))

		// Debug printouts
		t.Logf("Script: %+v", s)
		t.Logf("Script bytes (hex): %x", s.Bytes())

		// Try direct script
		result := DecodeMap(s)
		t.Logf("Result with script pointer: %+v", result)
		if result != nil {
			t.Logf("Result data: %+v", result.Data)
		}

		// The test expects this to succeed
		require.NotNil(t, result, "Expected non-nil result")
		if result != nil {
			require.Equal(t, MapCmdSet, result.Cmd)
			require.Equal(t, "bsocial", result.Data["app"])
			require.Equal(t, "post", result.Data["type"])
		}
	})

	t.Run("SET command with null values", func(t *testing.T) {
		// Reset test state before each subtest
		resetTestState()

		s := &script.Script{}
		t.Logf("Adding MapCmdSet: %s", MapCmdSet)
		_ = s.AppendPushData([]byte(MapCmdSet))
		t.Logf("Adding key 'key1'")
		_ = s.AppendPushData([]byte("key1"))
		t.Logf("Adding null value")
		_ = s.AppendPushData([]byte{0x00})

		// Debug printouts
		t.Logf("Script: %+v", s)
		t.Logf("Script bytes (hex): %x", s.Bytes())

		result := DecodeMap(s)
		t.Logf("Result for null values test: %+v", result)
		if result != nil {
			t.Logf("Result data: %+v", result.Data)
		}

		require.NotNil(t, result, "Expected non-nil result")
		if result != nil {
			require.Equal(t, MapCmdSet, result.Cmd)
			require.Equal(t, " ", result.Data["key1"])
		}
	})

	t.Run("SET command with missing value", func(t *testing.T) {
		// Reset test state before each subtest
		resetTestState()

		s := &script.Script{}
		t.Logf("Adding MapCmdSet: %s", MapCmdSet)
		_ = s.AppendPushData([]byte(MapCmdSet))
		t.Logf("Adding key 'key2'")
		_ = s.AppendPushData([]byte("key2"))
		// Intentionally missing value

		// Debug printouts
		t.Logf("Script: %+v", s)
		t.Logf("Script bytes (hex): %x", s.Bytes())

		result := DecodeMap(s)
		t.Logf("Result for missing value test: %+v", result)
		if result != nil {
			t.Logf("Result data: %+v", result.Data)
			t.Logf("Data keys length: %d", len(result.Data))
		}

		require.NotNil(t, result, "Expected non-nil result")
		if result != nil {
			require.Equal(t, MapCmdSet, result.Cmd)
			require.Empty(t, result.Data["key2"])
			require.Equal(t, 0, len(result.Data))
		}
	})
}

// TestDecodeMap_Bytes tests that DecodeMap can handle raw bytes input
func TestDecodeMap_Bytes(t *testing.T) {
	// Reset test state
	resetTestState()

	// Test nil input
	result := DecodeMap(nil)
	require.Nil(t, result, "Expected nil result for nil input")

	// Create a valid MAP protocol script
	s := &script.Script{}
	t.Logf("Adding MapCmdSet: %s", MapCmdSet)
	_ = s.AppendPushData([]byte(MapCmdSet))
	t.Logf("Adding key 'app'")
	_ = s.AppendPushData([]byte("app"))
	t.Logf("Adding value 'bsocial'")
	_ = s.AppendPushData([]byte("bsocial"))
	t.Logf("Adding key 'type'")
	_ = s.AppendPushData([]byte("type"))
	t.Logf("Adding value 'post'")
	_ = s.AppendPushData([]byte("post"))

	// Debug prints
	t.Logf("Script for bytes test: %+v", s)

	// Get the bytes
	scriptBytes := s.Bytes()
	t.Logf("Script bytes (hex): %x", scriptBytes)

	// Try with new script from bytes
	newScript := script.NewFromBytes(scriptBytes)
	t.Logf("New script from bytes: %+v", newScript)

	resultFromNewScript := DecodeMap(newScript)
	t.Logf("Result with newScript: %+v", resultFromNewScript)
	if resultFromNewScript != nil {
		t.Logf("Result data: %+v", resultFromNewScript.Data)
	}

	// Reset test state before the next test
	resetTestState()

	// Now try with raw bytes
	result = DecodeMap(scriptBytes)
	t.Logf("Result with raw bytes: %+v", result)
	if result != nil {
		t.Logf("Result data: %+v", result.Data)
	}

	// Reset test state before the next test
	resetTestState()

	// Try using a different approach to create the script bytes
	manualScript := &script.Script{}
	_ = manualScript.AppendPushData([]byte(MapCmdSet))
	_ = manualScript.AppendPushData([]byte("app"))
	_ = manualScript.AppendPushData([]byte("bsocial"))
	_ = manualScript.AppendPushData([]byte("type"))
	_ = manualScript.AppendPushData([]byte("post"))
	manualBytes := manualScript.Bytes()
	t.Logf("Manual script bytes (hex): %x", manualBytes)
	resultManual := DecodeMap(manualBytes)
	t.Logf("Result with manual bytes: %+v", resultManual)

	// The test expects this to work
	require.NotNil(t, result, "Expected non-nil result for valid script bytes")
	if result != nil {
		require.Equal(t, MapCmdSet, result.Cmd, "Expected correct command")
		require.Equal(t, "bsocial", result.Data["app"], "Expected correct app value")
		require.Equal(t, "post", result.Data["type"], "Expected correct type value")
	}

	// Reset test state before the next test
	resetTestState()

	// Test invalid script bytes
	invalidBytes := []byte{0x00, 0x01} // Just some random bytes
	result = DecodeMap(invalidBytes)
	// Invalid bytes should return nil since they don't have a proper prefix
	require.Nil(t, result, "Expected nil result for invalid script bytes")
}

// TestToScript tests the ToScript helper function directly
func TestToScript(t *testing.T) {
	// Reset test state
	resetTestState()

	// Create a valid MAP protocol script
	s := &script.Script{}
	_ = s.AppendPushData([]byte(MapPrefix))
	_ = s.AppendPushData([]byte(MapCmdSet))
	_ = s.AppendPushData([]byte("app"))
	_ = s.AppendPushData([]byte("bsocial"))

	// Test converting script to script
	scriptPtr := ToScript(s)
	require.NotNil(t, scriptPtr, "ToScript should handle *script.Script")
	require.Equal(t, s, scriptPtr, "ToScript should return the same script pointer")

	// Reset test state
	resetTestState()

	// Test converting script value to script
	scriptVal := *s
	scriptFromVal := ToScript(scriptVal)
	require.NotNil(t, scriptFromVal, "ToScript should handle script.Script")
	require.Equal(t, s.Bytes(), scriptFromVal.Bytes(), "Bytes should match")

	// Reset test state
	resetTestState()

	// Test converting bytes to script
	bytes := s.Bytes()
	t.Logf("Original script bytes: %x", bytes)
	scriptFromBytes := ToScript(bytes)
	require.NotNil(t, scriptFromBytes, "ToScript should handle []byte")
	t.Logf("scriptFromBytes: %+v", scriptFromBytes)
	t.Logf("scriptFromBytes bytes: %x", scriptFromBytes.Bytes())

	// Reset test state
	resetTestState()

	// Decode Map from different sources
	mapFromScript := DecodeMap(s)

	// Reset test state
	resetTestState()

	mapFromBytes := DecodeMap(bytes)

	t.Logf("mapFromScript: %+v", mapFromScript)
	t.Logf("mapFromBytes: %+v", mapFromBytes)

	// Check if both are non-nil
	require.NotNil(t, mapFromScript, "DecodeMap should work with script")
	require.NotNil(t, mapFromBytes, "DecodeMap should work with bytes")
}
