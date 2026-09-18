package ordfs

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	contentTypeJSONDir = "ord-fs/json"
	contentTypeDir     = "ordfs/dir"
	contentTypePatch   = "ordfs/patch"

	dirVersion = 0x01

	dirFlagReftype  = 1 << 3
	dirFlagReserved = 0xF0
)

var errInvalidDirectory = errors.New("invalid directory manifest")

func isDirectoryType(ct string) bool {
	return ct == contentTypeJSONDir || ct == contentTypeDir
}

func isPatchType(ct string) bool {
	return ct == contentTypePatch
}

func parseDirectory(contentType string, content []byte) (map[string]string, error) {
	switch contentType {
	case contentTypeJSONDir:
		var directory map[string]string
		if err := json.Unmarshal(content, &directory); err != nil {
			return nil, fmt.Errorf("invalid directory format: %w", err)
		}
		return directory, nil
	case contentTypeDir:
		return parseDir(content)
	default:
		return nil, errInvalidDirectory
	}
}

func parseDir(b []byte) (map[string]string, error) {
	if len(b) < 3 {
		return nil, errInvalidDirectory
	}
	if b[0] != dirVersion {
		return nil, errInvalidDirectory
	}
	n := int(binary.BigEndian.Uint16(b[1:3]))
	off := 3
	out := make(map[string]string, n)
	var prev []byte
	for i := 0; i < n; i++ {
		if off >= len(b) {
			return nil, errInvalidDirectory
		}
		flags := b[off]
		off++
		if flags&dirFlagReserved != 0 {
			return nil, errInvalidDirectory
		}
		if off >= len(b) {
			return nil, errInvalidDirectory
		}
		nameLen := int(b[off])
		off++
		if nameLen < 1 || off+nameLen > len(b) {
			return nil, errInvalidDirectory
		}
		name := b[off : off+nameLen]
		off += nameLen
		if bytes.IndexByte(name, 0) >= 0 || bytes.IndexByte(name, '/') >= 0 {
			return nil, errInvalidDirectory
		}
		if prev != nil && bytes.Compare(prev, name) >= 0 {
			return nil, errInvalidDirectory
		}
		prev = bytes.Clone(name)

		var pointer string
		if flags&dirFlagReftype == 0 {
			if off >= len(b) {
				return nil, errInvalidDirectory
			}
			pointer = fmt.Sprintf("_%d", b[off])
			off++
		} else {
			if off+outpointSize > len(b) {
				return nil, errInvalidDirectory
			}
			op := transaction.NewOutpointFromBytes(b[off : off+outpointSize])
			if op == nil {
				return nil, errInvalidDirectory
			}
			pointer = fmt.Sprintf("%s_%d", op.Txid.String(), op.Index)
			off += outpointSize
		}
		out[string(name)] = pointer
	}
	if off != len(b) {
		return nil, errInvalidDirectory
	}
	return out, nil
}
