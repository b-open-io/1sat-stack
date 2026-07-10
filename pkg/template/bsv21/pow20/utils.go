package pow20

import (
	"encoding/binary"

	"github.com/bsv-blockchain/go-sdk/util"
)

func uint64ToBytes(v uint64) []byte {
	val := make([]byte, 0, 8)
	max := binary.BigEndian.AppendUint64([]byte{}, v)
	for i, b := range max {
		if i < len(max)-1 && b == 0 && max[i+1]&0x80 == 0 && len(val) == 0 {
			continue
		}
		val = append(val, b)
	}
	return util.ReverseBytes(val)
}
