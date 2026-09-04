package ecosystemalias

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	// CursorPrefix is the versioned, URL-safe cursor prefix. Version 1.
	CursorPrefix = "ea1."
	// CursorVersion is the cursor format version encoded by CursorPrefix.
	CursorVersion = 1
	txidHexLen    = 64
)

// Cursor is a client-derived, opaque pagination token. It encodes the last
// returned outpoint plus a fingerprint of the normalized query mode/value.
// On receipt the service resolves that outpoint's stored sort key. No server
// secret is required; validation is structural and binding, not authorization.
//
// BRC-24 output-list hydration currently discards lookup Result metadata, so
// no cursor can be returned as overlay lookup metadata. Clients derive the
// next cursor from the last hydrated outpoint.
type Cursor struct {
	Version     int
	Mode        Mode
	Binding     string
	Txid        string
	Vout        uint32
	Encoded     string
	Fingerprint string
}

// NewCursor builds a versioned cursor bound to q and the last returned outpoint.
func NewCursor(q Query, txid string, vout uint32) (string, error) {
	fp, err := QueryFingerprint(q)
	if err != nil {
		return "", err
	}
	id, err := normalizeTxid(txid)
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf(`{"m":%q,"q":%q,"t":%q,"v":%d}`, q.Mode(), fp, id, vout)
	return CursorPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload)), nil
}

// ParseCursor structurally decodes a cursor without checking query binding.
func ParseCursor(raw string) (Cursor, error) {
	if raw == "" || !strings.HasPrefix(raw, CursorPrefix) {
		return Cursor{}, fail(CodeMalformedCursor, "cursor must use prefix "+CursorPrefix)
	}
	payload := strings.TrimPrefix(raw, CursorPrefix)
	if payload == "" || !isBase64URL(payload) {
		return Cursor{}, fail(CodeMalformedCursor, "cursor is not unpadded URL-safe base64")
	}
	bin, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Cursor{}, fail(CodeMalformedCursor, "cursor is not URL-safe base64")
	}
	dec := json.NewDecoder(bytes.NewReader(bin))
	dec.UseNumber()
	open, err := dec.Token()
	if err != nil {
		return Cursor{}, fail(CodeMalformedCursor, "cursor payload is not JSON")
	}
	d, ok := open.(json.Delim)
	if !ok || d != '{' {
		return Cursor{}, fail(CodeMalformedCursor, "cursor payload must be an object")
	}

	seen := map[string]bool{}
	var (
		mode    Mode
		binding string
		txid    string
		vout    uint32
		haveV   bool
	)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return Cursor{}, fail(CodeMalformedCursor, "cursor payload is malformed")
		}
		key, ok := keyTok.(string)
		if !ok {
			return Cursor{}, fail(CodeMalformedCursor, "cursor field names must be strings")
		}
		if seen[key] {
			return Cursor{}, fail(CodeMalformedCursor, "cursor has duplicate field "+key)
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return Cursor{}, fail(CodeMalformedCursor, "cursor payload is malformed")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Cursor{}, fail(CodeMalformedCursor, "cursor field "+key+" must not be null")
		}
		switch key {
		case "m":
			s, err := decodeJSONString(value, "m")
			if err != nil {
				return Cursor{}, fail(CodeMalformedCursor, "cursor mode must be a string")
			}
			switch Mode(s) {
			case ModeAlias, ModeDomain, ModeFindAll:
				mode = Mode(s)
			default:
				return Cursor{}, fail(CodeMalformedCursor, "cursor mode is unknown")
			}
		case "q":
			s, err := decodeJSONString(value, "q")
			if err != nil {
				return Cursor{}, fail(CodeMalformedCursor, "cursor fingerprint must be a string")
			}
			if !isHexLen(s, 64) {
				return Cursor{}, fail(CodeMalformedCursor, "cursor fingerprint is not SHA-256 hex")
			}
			binding = s
		case "t":
			s, err := decodeJSONString(value, "t")
			if err != nil {
				return Cursor{}, fail(CodeMalformedCursor, "cursor txid must be a string")
			}
			id, err := normalizeTxid(s)
			if err != nil {
				return Cursor{}, fail(CodeMalformedCursor, "cursor txid must be 64 lowercase hex characters")
			}
			txid = id
		case "v":
			n, err := decodeJSONVout(value)
			if err != nil {
				return Cursor{}, err
			}
			vout = n
			haveV = true
		default:
			return Cursor{}, fail(CodeMalformedCursor, "cursor has unknown field "+key)
		}
	}
	if _, err := dec.Token(); err != nil {
		return Cursor{}, fail(CodeMalformedCursor, "cursor payload is malformed")
	}
	if _, err := dec.Token(); err != io.EOF {
		return Cursor{}, fail(CodeMalformedCursor, "cursor payload has trailing JSON")
	}
	if mode == ModeNone || binding == "" || txid == "" || !haveV {
		return Cursor{}, fail(CodeMalformedCursor, "cursor payload is missing required fields")
	}
	return Cursor{
		Version:     CursorVersion,
		Mode:        mode,
		Binding:     binding,
		Txid:        txid,
		Vout:        vout,
		Encoded:     raw,
		Fingerprint: binding,
	}, nil
}

func isBase64URL(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') &&
			(c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// BindCursor parses raw and requires it to be bound to q's normalized mode/value.
func BindCursor(raw string, q Query) (Cursor, error) {
	cur, err := ParseCursor(raw)
	if err != nil {
		return Cursor{}, err
	}
	fp, err := QueryFingerprint(q)
	if err != nil {
		return Cursor{}, err
	}
	if cur.Mode != q.Mode() || cur.Fingerprint != fp {
		return Cursor{}, fail(CodeCursorMismatch, "cursor belongs to a different query")
	}
	return cur, nil
}

func decodeJSONVout(raw json.RawMessage) (uint32, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return 0, fail(CodeMalformedCursor, "cursor vout must be an integer")
	}
	num, ok := tok.(json.Number)
	if !ok || !isUintDigits(num.String()) {
		return 0, fail(CodeMalformedCursor, "cursor vout must be an integer")
	}
	n, err := num.Int64()
	if err != nil || n < 0 || n > int64(^uint32(0)) {
		return 0, fail(CodeMalformedCursor, "cursor vout is out of range")
	}
	return uint32(n), nil
}

func isUintDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func normalizeTxid(s string) (string, error) {
	if !isHexLen(s, txidHexLen) {
		return "", fail(CodeInvalidOutpoint, "txid must be 64 lowercase hex characters")
	}
	return s, nil
}

func isHexLen(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
