package shrug

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/stretchr/testify/require"
)

// Golden vector shared with the TypeScript implementation. RFC 8949 §4.2
// sorts by the bytewise order of the encoded keys (length header included),
// so 3-byte keys precede 4-byte keys: "dec" < "sym" < "icon".
func goldenMetadataHex() string {
	return "a3" + // map(3)
		"63646563" + "08" + // "dec": 8
		"6373796d" + "64474f4c44" + // "sym": "GOLD"
		"6469636f6e" + "5824" + strings.Repeat("11", 32) + "01000000" // "icon": 36-byte outpoint
}

func TestMetadata_EncodeGoldenVector(t *testing.T) {
	sym := "GOLD"
	dec := uint8(8)
	m := &Metadata{
		Symbol:   &sym,
		Icon:     testOutpoint(t),
		Decimals: &dec,
	}

	encoded, err := m.Encode()
	require.NoError(t, err)
	require.Equal(t, goldenMetadataHex(), hex.EncodeToString(encoded))

	decoded, err := DecodeMetadata(encoded)
	require.NoError(t, err)
	require.Equal(t, sym, *decoded.Symbol)
	require.Equal(t, dec, *decoded.Decimals)
	require.Equal(t, m.Icon.Bytes(), decoded.Icon.Bytes())
}

func TestMetadata_EmptyDocument(t *testing.T) {
	encoded, err := (&Metadata{}).Encode()
	require.NoError(t, err)
	require.Equal(t, "a0", hex.EncodeToString(encoded))

	decoded, err := DecodeMetadata(encoded)
	require.NoError(t, err)
	require.Nil(t, decoded.Symbol)
	require.Nil(t, decoded.Icon)
	require.Nil(t, decoded.Decimals)
}

func TestMetadata_UnknownKeysIgnored(t *testing.T) {
	// {"dec": 2, "foo": 1}
	data, err := hex.DecodeString("a2" + "63646563" + "02" + "63666f6f" + "01")
	require.NoError(t, err)

	decoded, err := DecodeMetadata(data)
	require.NoError(t, err)
	require.Equal(t, uint8(2), *decoded.Decimals)
}

func TestDecode_PopulatesMetadataFromInscription(t *testing.T) {
	sym := "GOLD"
	dec := uint8(8)
	meta := &Metadata{Symbol: &sym, Icon: testOutpoint(t), Decimals: &dec}
	content, err := meta.Encode()
	require.NoError(t, err)

	envelope, err := (&inscription.Inscription{
		File: inscription.File{Type: MetadataContentType, Content: content},
	}).Lock()
	require.NoError(t, err)

	// deploy output: prefix + metadata inscription + P2PKH
	in := &Shrug{ScriptSuffix: append(*envelope, testSuffix()...)}
	decoded := Decode(in.Lock())
	require.NotNil(t, decoded)
	require.NotNil(t, decoded.Insc)
	require.NotNil(t, decoded.Metadata)
	require.Equal(t, sym, *decoded.Metadata.Symbol)
	require.Equal(t, dec, *decoded.Metadata.Decimals)
	require.Equal(t, testOutpoint(t).Bytes(), decoded.Metadata.Icon.Bytes())
}

func TestDecode_NonMetadataInscription(t *testing.T) {
	envelope, err := (&inscription.Inscription{
		File: inscription.File{Type: "text/plain", Content: []byte("hello")},
	}).Lock()
	require.NoError(t, err)

	in := &Shrug{ScriptSuffix: append(*envelope, testSuffix()...)}
	decoded := Decode(in.Lock())
	require.NotNil(t, decoded)
	require.NotNil(t, decoded.Insc)
	require.Equal(t, "text/plain", decoded.Insc.File.Type)
	require.Nil(t, decoded.Metadata)
}

func TestMetadata_Invalid(t *testing.T) {
	t.Run("icon wrong length", func(t *testing.T) {
		// {"icon": h'11223344'}
		data, err := hex.DecodeString("a1" + "6469636f6e" + "44" + "11223344")
		require.NoError(t, err)
		_, err = DecodeMetadata(data)
		require.Error(t, err)
	})

	t.Run("dec out of range on decode", func(t *testing.T) {
		// {"dec": 19}
		data, err := hex.DecodeString("a1" + "63646563" + "13")
		require.NoError(t, err)
		_, err = DecodeMetadata(data)
		require.Error(t, err)
	})

	t.Run("dec out of range on encode", func(t *testing.T) {
		dec := uint8(19)
		_, err := (&Metadata{Decimals: &dec}).Encode()
		require.Error(t, err)
	})

	t.Run("not a map", func(t *testing.T) {
		_, err := DecodeMetadata([]byte{0x01})
		require.Error(t, err)
	})
}
