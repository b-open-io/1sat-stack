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
	// DirContentType is the binary directory manifest content type.
	DirContentType = "ordfs/dir"
	// JSONDirContentType is the legacy JSON manifest content type.
	JSONDirContentType = "ord-fs/json"

	contentTypeJSONDir = JSONDirContentType
	contentTypeDir     = DirContentType
	contentTypePatch   = "ordfs/patch"

	dirVersion = 0x01

	dirFlagDir      = 1 << 0
	dirFlagExec     = 1 << 1
	dirFlagSymlink  = 1 << 2
	dirFlagReftype  = 1 << 3
	dirFlagReserved = 0xF0
)

// ErrInvalidDirectory is returned for a manifest that is malformed or not in
// canonical form. Readers reject anything else; see DecodeDir.
var ErrInvalidDirectory = errors.New("invalid directory manifest")

// DirEntry is one decoded `ordfs/dir` entry. Exactly one of Vout and Outpoint
// carries the target: Vout >= 0 names a sibling output of the transaction
// holding the manifest, otherwise Outpoint names it outright.
type DirEntry struct {
	Name     string
	IsDir    bool
	Exec     bool
	Symlink  bool
	Vout     int
	Outpoint *transaction.Outpoint
}

// Target resolves the entry against the transaction holding the manifest.
func (e DirEntry) Target(manifest *transaction.Outpoint) *transaction.Outpoint {
	if e.Outpoint != nil {
		return e.Outpoint
	}
	if manifest == nil || e.Vout < 0 {
		return nil
	}
	return &transaction.Outpoint{Txid: manifest.Txid, Index: uint32(e.Vout)}
}

// DecodeDir decodes a binary `ordfs/dir` manifest. Non-canonical input (bad
// version, unsorted or duplicate names, reserved flag bits, trailing bytes)
// is rejected rather than repaired.
func DecodeDir(b []byte) ([]DirEntry, error) {
	if len(b) < 3 {
		return nil, ErrInvalidDirectory
	}
	if b[0] != dirVersion {
		return nil, ErrInvalidDirectory
	}
	n := int(binary.BigEndian.Uint16(b[1:3]))
	off := 3
	out := make([]DirEntry, 0, n)
	var prev []byte
	for i := 0; i < n; i++ {
		if off >= len(b) {
			return nil, ErrInvalidDirectory
		}
		flags := b[off]
		off++
		if flags&dirFlagReserved != 0 {
			return nil, ErrInvalidDirectory
		}
		if off >= len(b) {
			return nil, ErrInvalidDirectory
		}
		nameLen := int(b[off])
		off++
		if nameLen < 1 || off+nameLen > len(b) {
			return nil, ErrInvalidDirectory
		}
		name := b[off : off+nameLen]
		off += nameLen
		if bytes.IndexByte(name, 0) >= 0 || bytes.IndexByte(name, '/') >= 0 {
			return nil, ErrInvalidDirectory
		}
		if prev != nil && bytes.Compare(prev, name) >= 0 {
			return nil, ErrInvalidDirectory
		}
		prev = bytes.Clone(name)

		entry := DirEntry{
			Name:    string(name),
			IsDir:   flags&dirFlagDir != 0,
			Exec:    flags&dirFlagExec != 0,
			Symlink: flags&dirFlagSymlink != 0,
			Vout:    -1,
		}
		if flags&dirFlagReftype == 0 {
			if off >= len(b) {
				return nil, ErrInvalidDirectory
			}
			entry.Vout = int(b[off])
			off++
		} else {
			if off+outpointSize > len(b) {
				return nil, ErrInvalidDirectory
			}
			op := transaction.NewOutpointFromBytes(b[off : off+outpointSize])
			if op == nil {
				return nil, ErrInvalidDirectory
			}
			entry.Outpoint = op
			off += outpointSize
		}
		out = append(out, entry)
	}
	if off != len(b) {
		return nil, ErrInvalidDirectory
	}
	return out, nil
}

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
		return nil, ErrInvalidDirectory
	}
}

func parseDir(b []byte) (map[string]string, error) {
	entries, err := DecodeDir(b)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.Outpoint != nil {
			out[e.Name] = fmt.Sprintf("%s_%d", e.Outpoint.Txid.String(), e.Outpoint.Index)
			continue
		}
		out[e.Name] = fmt.Sprintf("_%d", e.Vout)
	}
	return out, nil
}
