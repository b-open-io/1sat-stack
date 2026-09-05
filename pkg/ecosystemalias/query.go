package ecosystemalias

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// DecodeQuery strictly decodes a BRC-24 query object for ls_ecosystemalias.
// Unknown fields, JSON null, malformed JSON, invalid combinations,
// findAll:false, empty or illegal values, zero/oversized limits, and
// negative skips are rejected with typed codes.
func DecodeQuery(raw json.RawMessage) (Query, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return Query{}, fail(CodeMalformedJSON, "query JSON is empty")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return Query{}, fail(CodeJSONNull, "query JSON must not be null")
	}
	if trimmed[0] != '{' {
		return Query{}, fail(CodeMalformedJSON, "query JSON must be an object")
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()

	open, err := dec.Token()
	if err != nil {
		return Query{}, fail(CodeMalformedJSON, "malformed query JSON")
	}
	d, ok := open.(json.Delim)
	if !ok || d != '{' {
		return Query{}, fail(CodeMalformedJSON, "query JSON must be an object")
	}

	seen := make(map[string]bool, 5)
	var q Query
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return Query{}, fail(CodeMalformedJSON, "malformed query JSON")
		}
		key, ok := keyTok.(string)
		if !ok {
			return Query{}, fail(CodeMalformedJSON, "query field names must be strings")
		}
		if seen[key] {
			return Query{}, fail(CodeDuplicateField, "duplicate field "+key)
		}
		seen[key] = true

		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return Query{}, fail(CodeMalformedJSON, "malformed query JSON")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Query{}, fail(CodeJSONNull, key+" must not be null")
		}

		switch key {
		case "alias":
			s, err := decodeJSONString(value, "alias")
			if err != nil {
				return Query{}, err
			}
			norm, err := NormalizeAliasQuery(s)
			if err != nil {
				return Query{}, err
			}
			q.Alias = &norm
		case "domain":
			s, err := decodeJSONString(value, "domain")
			if err != nil {
				return Query{}, err
			}
			norm, err := NormalizeDomainQuery(s)
			if err != nil {
				return Query{}, err
			}
			q.Domain = &norm
		case "findAll":
			v, err := decodeJSONBool(value, "findAll")
			if err != nil {
				return Query{}, err
			}
			if !v {
				return Query{}, fail(CodeFindAllFalse, "findAll must be true when present")
			}
			q.FindAll = boolPtr(true)
		case "limit":
			n, err := decodeJSONLimit(value)
			if err != nil {
				return Query{}, err
			}
			q.Limit = &n
		case "skip":
			n, err := decodeJSONSkip(value)
			if err != nil {
				return Query{}, err
			}
			q.Skip = &n
		default:
			return Query{}, fail(CodeUnknownField, "unknown field "+key)
		}
	}
	if _, err := dec.Token(); err != nil {
		return Query{}, fail(CodeMalformedJSON, "malformed query JSON")
	}
	if err := consumeEOF(dec); err != nil {
		return Query{}, err
	}

	mode := q.Mode()
	if mode == ModeNone {
		return Query{}, fail(CodeInvalidCombination, "query must have exactly one of alias, domain, or findAll:true")
	}
	return q, nil
}

func decodeJSONString(raw json.RawMessage, field string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return "", fail(CodeMalformedJSON, field+" must be a string")
	}
	s, ok := tok.(string)
	if !ok {
		return "", fail(CodeMalformedJSON, field+" must be a string")
	}
	if err := consumeEOF(dec); err != nil {
		return "", fail(CodeMalformedJSON, field+" must be a string")
	}
	return s, nil
}

func decodeJSONBool(raw json.RawMessage, field string) (bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return false, fail(CodeMalformedJSON, field+" must be a boolean")
	}
	v, ok := tok.(bool)
	if !ok {
		return false, fail(CodeMalformedJSON, field+" must be a boolean")
	}
	if err := consumeEOF(dec); err != nil {
		return false, fail(CodeMalformedJSON, field+" must be a boolean")
	}
	return v, nil
}

func decodeJSONLimit(raw json.RawMessage) (uint32, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return 0, fail(CodeMalformedJSON, "limit must be an integer")
	}
	num, ok := tok.(json.Number)
	if !ok {
		return 0, fail(CodeMalformedJSON, "limit must be an integer")
	}
	if err := consumeEOF(dec); err != nil {
		return 0, fail(CodeMalformedJSON, "limit must be an integer")
	}
	if strings.ContainsAny(num.String(), ".eE+") {
		return 0, fail(CodeMalformedJSON, "limit must be an integer")
	}
	n, err := num.Int64()
	if err != nil {
		return 0, fail(CodeMalformedJSON, "limit must be an integer")
	}
	if n <= 0 {
		return 0, fail(CodeLimitZero, "limit must be greater than zero")
	}
	if n > int64(MaxLimit) {
		return 0, fail(CodeLimitTooLarge, fmt.Sprintf("limit must be at most %d", MaxLimit))
	}
	return uint32(n), nil
}

func decodeJSONSkip(raw json.RawMessage) (uint32, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return 0, fail(CodeMalformedJSON, "skip must be an integer")
	}
	num, ok := tok.(json.Number)
	if !ok {
		return 0, fail(CodeMalformedJSON, "skip must be an integer")
	}
	if err := consumeEOF(dec); err != nil {
		return 0, fail(CodeMalformedJSON, "skip must be an integer")
	}
	if strings.ContainsAny(num.String(), ".eE+") {
		return 0, fail(CodeMalformedJSON, "skip must be an integer")
	}
	n, err := num.Int64()
	if err != nil {
		return 0, fail(CodeMalformedJSON, "skip must be an integer")
	}
	if n < 0 {
		return 0, fail(CodeSkipNegative, "skip must not be negative")
	}
	return uint32(n), nil
}

func consumeEOF(dec *json.Decoder) error {
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return fail(CodeMalformedJSON, "unexpected trailing JSON")
		}
		if err != io.EOF {
			return fail(CodeMalformedJSON, "malformed query JSON")
		}
	}
	return nil
}

func boolPtr(v bool) *bool { return &v }
