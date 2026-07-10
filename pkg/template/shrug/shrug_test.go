package shrug

import (
	"bytes"
	"encoding/hex"
	"math"
	"math/big"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/stretchr/testify/require"
)

func testSuffix() []byte {
	// P2PKH: OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
	suffix := []byte{0x76, 0xa9, 0x14}
	suffix = append(suffix, bytes.Repeat([]byte{0xab}, 20)...)
	return append(suffix, 0x88, 0xac)
}

func testOutpoint(t *testing.T) *transaction.Outpoint {
	t.Helper()
	b := bytes.Repeat([]byte{0x11}, 32)
	b = append(b, 0x01, 0x00, 0x00, 0x00) // vout 1
	outpoint := transaction.NewOutpointFromBytes(b)
	require.NotNil(t, outpoint)
	return outpoint
}

func TestLockAndDecode_RoundTrip(t *testing.T) {
	suffix := testSuffix()
	outpoint := testOutpoint(t)

	cases := []struct {
		name string
		in   *Shrug
	}{
		{"deploy with supply", &Shrug{Amount: big.NewInt(21_000_000), ScriptSuffix: suffix}},
		{"deploy authority", &Shrug{ScriptSuffix: suffix}},
		{"authority", &Shrug{Id: outpoint, ScriptSuffix: suffix}},
		{"value", &Shrug{Id: outpoint, Amount: big.NewInt(5000), ScriptSuffix: suffix}},
		{"amount 1", &Shrug{Id: outpoint, Amount: big.NewInt(1), ScriptSuffix: suffix}},
		{"max uint64", &Shrug{Id: outpoint, Amount: new(big.Int).SetUint64(math.MaxUint64), ScriptSuffix: suffix}},
		{"beyond uint64", &Shrug{Id: outpoint, Amount: new(big.Int).Lsh(big.NewInt(1), 128), ScriptSuffix: suffix}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := Decode(tc.in.Lock())
			require.NotNil(t, decoded)

			expected := tc.in.Amount
			if expected == nil {
				expected = big.NewInt(0)
			}
			require.NotNil(t, decoded.Amount)
			require.Zero(t, expected.Cmp(decoded.Amount))

			if tc.in.Id == nil {
				require.Nil(t, decoded.Id)
			} else {
				require.NotNil(t, decoded.Id)
				require.Equal(t, tc.in.Id.Bytes(), decoded.Id.Bytes())
			}
			require.Equal(t, tc.in.ScriptSuffix, decoded.ScriptSuffix)
		})
	}
}

func TestDecode_InvalidScripts(t *testing.T) {
	suffix := testSuffix()
	outpoint := testOutpoint(t)

	push := func(s *script.Script, data []byte) {
		require.NoError(t, s.AppendPushData(data))
	}
	ops := func(s *script.Script, o ...byte) {
		require.NoError(t, s.AppendOpcodes(o...))
	}

	t.Run("not enough ops", func(t *testing.T) {
		s := script.Script([]byte{0x00, 0x01})
		require.Nil(t, Decode(&s))
	})

	t.Run("wrong tag", func(t *testing.T) {
		s := &script.Script{}
		push(s, []byte("notashrug"))
		require.Nil(t, Decode(s))
	})

	t.Run("truncated after tag", func(t *testing.T) {
		s := &script.Script{}
		push(s, []byte(SHRUG_TAG))
		require.Nil(t, Decode(s))
	})

	t.Run("id wrong length", func(t *testing.T) {
		s := &script.Script{}
		push(s, []byte(SHRUG_TAG))
		push(s, bytes.Repeat([]byte{0x11}, 35))
		ops(s, script.Op2DROP)
		push(s, []byte{0x01})
		ops(s, script.OpDROP)
		require.Nil(t, Decode(s))
	})

	t.Run("missing 2DROP", func(t *testing.T) {
		s := &script.Script{}
		push(s, []byte(SHRUG_TAG))
		ops(s, script.Op0, script.OpDROP)
		push(s, []byte{0x01})
		ops(s, script.OpDROP)
		require.Nil(t, Decode(s))
	})

	t.Run("missing final DROP", func(t *testing.T) {
		s := &script.Script{}
		push(s, []byte(SHRUG_TAG))
		ops(s, script.Op0, script.Op2DROP)
		push(s, []byte{0x01})
		require.Nil(t, Decode(s))
	})

	t.Run("negative amount", func(t *testing.T) {
		s := &script.Script{}
		push(s, []byte(SHRUG_TAG))
		push(s, outpoint.Bytes())
		ops(s, script.Op2DROP)
		push(s, []byte{0x81}) // -1
		ops(s, script.OpDROP)
		require.Nil(t, Decode(s))
	})

	t.Run("non-push amount opcode", func(t *testing.T) {
		s := &script.Script{}
		push(s, []byte(SHRUG_TAG))
		push(s, outpoint.Bytes())
		ops(s, script.Op2DROP, script.Op1, script.OpDROP)
		require.Nil(t, Decode(s))
	})

	t.Run("valid prefix with suffix intact", func(t *testing.T) {
		in := &Shrug{Id: outpoint, Amount: big.NewInt(42), ScriptSuffix: suffix}
		decoded := Decode(in.Lock())
		require.NotNil(t, decoded)
		require.Equal(t, suffix, decoded.ScriptSuffix)
	})
}

// Golden prefix vector shared with the TypeScript implementation. The txid
// bytes are asymmetric so byte-order mistakes cannot round-trip silently.
func TestLock_GoldenPrefix(t *testing.T) {
	b := make([]byte, 36)
	for i := range 32 {
		b[i] = byte(i)
	}
	b[32] = 0x01 // vout 1, little-endian
	outpoint := transaction.NewOutpointFromBytes(b)
	require.NotNil(t, outpoint)

	in := &Shrug{Id: outpoint, Amount: big.NewInt(5000)}
	expected := "0d" + "c2af5c5f28e38384295f2fc2af" + // push 13-byte tag
		"24" + "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" + "01000000" + // push 36-byte outpoint
		"6d" + // OP_2DROP
		"02" + "8813" + // push amount 5000 as script number (LE)
		"75" // OP_DROP
	require.Equal(t, expected, hex.EncodeToString(*in.Lock()))
}
