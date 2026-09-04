package ecosystemalias

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const asciiWhitespace = " \t\n\r\f\v"

// NormalizeAliasQuery ASCII-lowercases a lookup alias. It rejects leading or
// trailing whitespace, non-ASCII input, empty values, and grammar violations.
func NormalizeAliasQuery(s string) (string, error) {
	if err := rejectQueryText(s); err != nil {
		return "", err
	}
	lower := asciiLower(s)
	if err := aliasGrammar(lower); err != nil {
		return "", err
	}
	return lower, nil
}

// NormalizeDomainQuery ASCII-lowercases a lookup domain. It rejects leading or
// trailing whitespace, non-ASCII/Unicode input, empty values, and RFC 1123
// FQDN grammar violations. Valid xn-- punycode labels are accepted as ASCII.
func NormalizeDomainQuery(s string) (string, error) {
	if err := rejectQueryText(s); err != nil {
		return "", err
	}
	lower := asciiLower(s)
	if err := domainGrammar(lower); err != nil {
		return "", err
	}
	return lower, nil
}

// ValidateTokenAlias checks an already-signed alias. Token values must already
// be normalized; this does not case-fold.
func ValidateTokenAlias(s string) error {
	if s == "" {
		return fail(CodeEmptyValue, "alias is empty")
	}
	if hasNonASCII(s) {
		return fail(CodeNonASCII, "alias must be ASCII")
	}
	if hasASCIIWrapSpace(s) {
		return fail(CodeLeadingTrailingWhitespace, "alias must not have leading or trailing whitespace")
	}
	if s != asciiLower(s) {
		return fail(CodeUnnormalizedToken, "token alias must already be lowercase")
	}
	return aliasGrammar(s)
}

// ValidateTokenDomain checks an already-signed domain. Token values must
// already be normalized; this does not case-fold. Unicode is rejected.
func ValidateTokenDomain(s string) error {
	if s == "" {
		return fail(CodeEmptyValue, "domain is empty")
	}
	if hasNonASCII(s) {
		return fail(CodeNonASCII, "domain must be ASCII; Unicode must be punycode")
	}
	if hasASCIIWrapSpace(s) {
		return fail(CodeLeadingTrailingWhitespace, "domain must not have leading or trailing whitespace")
	}
	if s != asciiLower(s) {
		return fail(CodeUnnormalizedToken, "token domain must already be lowercase")
	}
	return domainGrammar(s)
}

func rejectQueryText(s string) error {
	if s == "" {
		return fail(CodeEmptyValue, "value is empty")
	}
	if hasNonASCII(s) {
		return fail(CodeNonASCII, "value must be ASCII; Unicode is rejected")
	}
	if hasASCIIWrapSpace(s) {
		return fail(CodeLeadingTrailingWhitespace, "value must not have leading or trailing whitespace")
	}
	return nil
}

func hasASCIIWrapSpace(s string) bool {
	if s == "" {
		return false
	}
	return strings.ContainsRune(asciiWhitespace, rune(s[0])) || strings.ContainsRune(asciiWhitespace, rune(s[len(s)-1]))
}

func hasNonASCII(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func aliasGrammar(s string) error {
	if len(s) < 1 || len(s) > MaxAliasBytes {
		return fail(CodeInvalidAlias, "alias must be 1 to 32 bytes")
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fail(CodeInvalidAlias, "alias must not start or end with a hyphen")
	}
	prevHyphen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			prevHyphen = false
		case c == '-':
			if prevHyphen {
				return fail(CodeInvalidAlias, "alias must not contain consecutive hyphens")
			}
			prevHyphen = true
		default:
			return fail(CodeInvalidAlias, "alias may contain only lowercase letters, digits, and internal single hyphens")
		}
	}
	return nil
}

func domainGrammar(s string) error {
	if s == "" {
		return fail(CodeEmptyValue, "domain is empty")
	}
	if len(s) > MaxDomainBytes {
		return fail(CodeInvalidDomain, "domain must be at most 253 bytes")
	}
	if strings.HasSuffix(s, ".") {
		return fail(CodeInvalidDomain, "domain must not have a trailing dot")
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return fail(CodeInvalidDomain, "domain must have at least two labels")
	}
	for _, label := range labels {
		if err := domainLabel(label); err != nil {
			return err
		}
	}
	return nil
}

func domainLabel(s string) error {
	if len(s) < 1 || len(s) > MaxLabelBytes {
		return fail(CodeInvalidDomain, "domain label must be 1 to 63 bytes")
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fail(CodeInvalidDomain, "domain label must not start or end with a hyphen")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fail(CodeInvalidDomain, "domain labels must be lowercase RFC 1123")
		}
	}
	return nil
}
