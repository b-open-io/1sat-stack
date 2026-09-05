// Package ecosystemalias freezes the BRC-169 ecosystem-alias overlay contract.
//
// This package is contract-only: it defines parser/query interfaces,
// normalization, typed errors, HeightScore ordering, and conformance vectors.
// Lookup indexes overlay events; OPL-4445 implements decoding, topic, routes.
package ecosystemalias

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"github.com/b-open-io/1sat-stack/pkg/types"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

const (
	// TopicName is the BRC-87 topic manager name claimed by BRC-169 §5.5.
	TopicName = "tm_ecosystemalias"
	// LookupName is the BRC-87 lookup service name claimed by BRC-169 §5.5.
	LookupName = "ls_ecosystemalias"
	// ProtocolName is PushDrop field 1: the ASCII protocol identifier.
	ProtocolName = "ecosystem-alias"
	// ProtocolVersion is PushDrop field 2: the ASCII protocol version.
	ProtocolVersion = "1"
	// FieldCount is the required number of BRC-169 PushDrop fields.
	FieldCount = 6
	// CertifierKeyLen is the compressed secp256k1 public-key size in bytes.
	CertifierKeyLen = 33
	// DefaultLimit is the default lookup page size.
	DefaultLimit uint32 = 100
	// MaxLimit is the maximum lookup page size.
	MaxLimit uint32 = 500
	// MaxAliasBytes is the alias length limit (bytes).
	MaxAliasBytes = 32
	// MaxDomainBytes is the FQDN length limit without a trailing dot.
	MaxDomainBytes = 253
	// MaxLabelBytes is the RFC 1123 label length limit.
	MaxLabelBytes = 63
	// LookupHTTPPath is the only planned HTTP surface. Do not invent REST routes.
	LookupHTTPPath = "/ecosystemalias/overlay/lookup"
)

const (
	FieldProtocol  = 0
	FieldVersion   = 1
	FieldAlias     = 2
	FieldDomain    = 3
	FieldCertifier = 4
	FieldSignature = 5
)

// Query is the BRC-24 lookup query object for ls_ecosystemalias.
// Alias, Domain, or neither (empty object / skip+limit only) is a valid mode.
type Query struct {
	Alias  *string
	Domain *string
	Limit  *uint32
	Skip   *uint32
}

// Claim is a BRC-169 alias advertisement after the six PushDrop fields are decoded.
// Alias and Domain must already be normalized; they are signed as token values.
// Conflicts remain queryable; uniqueness is never imposed on alias or domain.
type Claim struct {
	Alias        string
	Domain       string
	CertifierKey [33]byte
	Signature    []byte
}

// Mode is the exclusive lookup mode of a Query.
type Mode string

const (
	ModeNone   Mode = ""
	ModeAlias  Mode = "alias"
	ModeDomain Mode = "domain"
	ModeAll    Mode = "*"
)

// Code is a stable typed error code, independent of the human-readable message.
type Code string

const (
	CodeMalformedJSON             Code = "malformed-json"
	CodeJSONNull                  Code = "json-null"
	CodeUnknownField              Code = "unknown-field"
	CodeDuplicateField            Code = "duplicate-field"
	CodeInvalidCombination        Code = "invalid-combination"
	CodeEmptyValue                Code = "empty-value"
	CodeInvalidAlias              Code = "invalid-alias"
	CodeInvalidDomain             Code = "invalid-domain"
	CodeLeadingTrailingWhitespace Code = "leading-trailing-whitespace"
	CodeNonASCII                  Code = "non-ascii"
	CodeLimitZero                 Code = "limit-zero"
	CodeLimitTooLarge             Code = "limit-too-large"
	CodeSkipNegative              Code = "skip-negative"
	CodeInvalidProtocol           Code = "invalid-protocol"
	CodeInvalidVersion            Code = "invalid-version"
	CodeFieldCount                Code = "field-count"
	CodeUnnormalizedToken         Code = "unnormalized-token"
	CodeInvalidCertifier          Code = "invalid-certifier"
	CodeInvalidSignature          Code = "invalid-signature"
	CodeNonPositiveValue          Code = "non-positive-value"
	CodeInvalidOutpoint           Code = "invalid-outpoint"
)

// Error is a contract error with a stable code separate from its message.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

func fail(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// CodeOf reports the typed contract code, if err is or wraps *Error.
func CodeOf(err error) (Code, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}

// PageLimit returns the query page size, applying the default when Limit is omitted.
func (q Query) PageLimit() uint32 {
	if q.Limit == nil {
		return DefaultLimit
	}
	return *q.Limit
}

// PageSkip returns the query offset, zero when Skip is omitted.
func (q Query) PageSkip() uint32 {
	if q.Skip == nil {
		return 0
	}
	return *q.Skip
}

// Mode returns the exclusive query mode. Invalid combinations return ModeNone.
func (q Query) Mode() Mode {
	n := 0
	mode := ModeNone
	if q.Alias != nil {
		n++
		mode = ModeAlias
	}
	if q.Domain != nil {
		n++
		mode = ModeDomain
	}
	if n == 0 {
		return ModeAll
	}
	if n != 1 {
		return ModeNone
	}
	return mode
}

// BindingValue returns the normalized alias or domain on the query.
func (q Query) BindingValue() string {
	switch q.Mode() {
	case ModeAlias:
		if q.Alias != nil {
			return *q.Alias
		}
	case ModeDomain:
		if q.Domain != nil {
			return *q.Domain
		}
	}
	return ""
}

// Digest is SHA-256 of the raw concatenation of BRC-169 fields 1–5
// (protocol, version, alias, domain, compressed certifier key) with no
// separators or length prefixes.
func Digest(alias, domain string, certifier [33]byte) [32]byte {
	return DigestFields(ProtocolName, ProtocolVersion, alias, domain, certifier)
}

// DigestFields hashes the five signed fields in order.
func DigestFields(protocol, version, alias, domain string, certifier [33]byte) [32]byte {
	var b bytes.Buffer
	b.Grow(len(protocol) + len(version) + len(alias) + len(domain) + len(certifier))
	b.WriteString(protocol)
	b.WriteString(version)
	b.WriteString(alias)
	b.WriteString(domain)
	b.Write(certifier[:])
	return sha256.Sum256(b.Bytes())
}

// Preimage returns the exact bytes hashed by DigestFields.
func Preimage(protocol, version, alias, domain string, certifier [33]byte) []byte {
	out := make([]byte, 0, len(protocol)+len(version)+len(alias)+len(domain)+len(certifier))
	out = append(out, protocol...)
	out = append(out, version...)
	out = append(out, alias...)
	out = append(out, domain...)
	out = append(out, certifier[:]...)
	return out
}

// ValidateSats enforces that a claim output has a positive satoshi value.
func ValidateSats(satoshis uint64) error {
	if satoshis == 0 {
		return fail(CodeNonPositiveValue, "claim output must have a positive satoshi value")
	}
	return nil
}

// ValidateTokenFields checks the six already-decoded BRC-48 PushDrop fields.
// It does not decode scripts; a strict local BRC-48 decoder is an OPL-4445 requirement.
func ValidateTokenFields(fields [][]byte) (*Claim, error) {
	if len(fields) != FieldCount {
		return nil, fail(CodeFieldCount, fmt.Sprintf("token must have exactly %d fields, got %d", FieldCount, len(fields)))
	}
	if string(fields[FieldProtocol]) != ProtocolName {
		return nil, fail(CodeInvalidProtocol, "protocol must be ecosystem-alias")
	}
	if string(fields[FieldVersion]) != ProtocolVersion {
		return nil, fail(CodeInvalidVersion, "version must be 1")
	}
	alias := string(fields[FieldAlias])
	if err := ValidateTokenAlias(alias); err != nil {
		return nil, err
	}
	domain := string(fields[FieldDomain])
	if err := ValidateTokenDomain(domain); err != nil {
		return nil, err
	}
	key, err := parseCertifierKey(fields[FieldCertifier])
	if err != nil {
		return nil, err
	}
	sig := fields[FieldSignature]
	if err := ValidateDERSignature(sig); err != nil {
		return nil, err
	}
	claim := &Claim{
		Alias:        alias,
		Domain:       domain,
		CertifierKey: key,
		Signature:    append([]byte(nil), sig...),
	}
	return claim, nil
}

func parseCertifierKey(b []byte) ([33]byte, error) {
	var key [33]byte
	if len(b) != CertifierKeyLen {
		return key, fail(CodeInvalidCertifier, "certifier key must be 33 compressed bytes")
	}
	if b[0] != 0x02 && b[0] != 0x03 {
		return key, fail(CodeInvalidCertifier, "certifier key must be a compressed secp256k1 public key")
	}
	if new(big.Int).SetBytes(b[1:]).Cmp(ec.S256().Params().P) >= 0 {
		return key, fail(CodeInvalidCertifier, "certifier key x-coordinate must be within the secp256k1 field")
	}
	if _, err := ec.ParsePubKey(b); err != nil {
		return key, fail(CodeInvalidCertifier, "certifier key must encode a valid secp256k1 curve point")
	}
	copy(key[:], b)
	return key, nil
}

// ValidateDERSignature checks that sig is a strict DER-encoded ECDSA signature.
// It does not verify the signature against a digest; verification is OPL-4445.
func ValidateDERSignature(sig []byte) error {
	if len(sig) < 8 || len(sig) > 72 {
		return fail(CodeInvalidSignature, "signature must be DER-encoded ECDSA")
	}
	if sig[0] != 0x30 {
		return fail(CodeInvalidSignature, "signature must be a DER sequence")
	}
	seqLen := int(sig[1])
	if seqLen != len(sig)-2 {
		return fail(CodeInvalidSignature, "signature DER length mismatch")
	}
	rest := sig[2:]
	r, rest, err := parseDERInt(rest)
	if err != nil {
		return err
	}
	s, rest, err := parseDERInt(rest)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fail(CodeInvalidSignature, "signature has trailing DER bytes")
	}
	if len(r) == 0 || len(s) == 0 {
		return fail(CodeInvalidSignature, "signature integers must be non-empty")
	}
	return nil
}

func parseDERInt(b []byte) (val []byte, rest []byte, err error) {
	if len(b) < 2 || b[0] != 0x02 {
		return nil, nil, fail(CodeInvalidSignature, "signature is not a DER integer")
	}
	n := int(b[1])
	if n == 0 || n > 33 || 2+n > len(b) {
		return nil, nil, fail(CodeInvalidSignature, "signature integer length is invalid")
	}
	val = b[2 : 2+n]
	if val[0]&0x80 != 0 {
		return nil, nil, fail(CodeInvalidSignature, "signature integer is negative")
	}
	if n > 1 && val[0] == 0x00 && val[1]&0x80 == 0 {
		return nil, nil, fail(CodeInvalidSignature, "signature integer is non-minimally encoded")
	}
	return val, b[2+n:], nil
}

// Placement is an event-score sort key. Score is overlay HeightScore
// (confirmed: height + txIndex/1e9; unconfirmed: ingest unix seconds).
// Vout breaks ties when two outputs share a score (same transaction).
type Placement struct {
	Score float64
	Vout  uint32
}

// EventScore is types.HeightScore: the value stored on overlay events.
func EventScore(height uint32, txIndex uint64) float64 {
	return types.HeightScore(height, txIndex)
}

// CompareLookup orders alias, domain, and empty-query results by event score, then vout.
func CompareLookup(a, b Placement) int {
	if a.Score < b.Score {
		return -1
	}
	if a.Score > b.Score {
		return 1
	}
	if a.Vout < b.Vout {
		return -1
	}
	if a.Vout > b.Vout {
		return 1
	}
	return 0
}
