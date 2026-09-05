package config

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
)

const (
	// EcosystemAliasMinConcurrency and EcosystemAliasMaxConcurrency bound the
	// number of simultaneous queue handlers created by one module instance.
	EcosystemAliasMinConcurrency = 1
	EcosystemAliasMaxConcurrency = 64

	// EcosystemAliasMinBatchSize and EcosystemAliasMaxBatchSize bound one
	// JungleBus-to-store batch so a malformed setting cannot create an
	// unbounded in-memory transaction slice.
	EcosystemAliasMinBatchSize = 1
	EcosystemAliasMaxBatchSize = 10000
)

// ValidateEcosystemAliasConcurrency validates the supported worker range.
func ValidateEcosystemAliasConcurrency(value int) error {
	return validateEcosystemAliasBound("concurrency", value, EcosystemAliasMinConcurrency, EcosystemAliasMaxConcurrency)
}

// ValidateEcosystemAliasBatchSize validates the supported ingestion range.
func ValidateEcosystemAliasBatchSize(value int) error {
	return validateEcosystemAliasBound("batch size", value, EcosystemAliasMinBatchSize, EcosystemAliasMaxBatchSize)
}

func validateEcosystemAliasBound(name string, value, minValue, maxValue int) error {
	if value < minValue || value > maxValue {
		return fmt.Errorf("ecosystem-alias %s must be between %d and %d", name, minValue, maxValue)
	}
	return nil
}

// ParseEcosystemAliasBoundedInt rejects non-integers and values outside the
// supplied positive range. It never converts invalid input into a zero value.
func ParseEcosystemAliasBoundedInt(name, raw string, minValue, maxValue int) (int, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return 0, fmt.Errorf("ecosystem-alias %s must be an integer between %d and %d", name, minValue, maxValue)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("ecosystem-alias %s must be an integer between %d and %d: %w", name, minValue, maxValue, err)
	}
	if err := validateEcosystemAliasBound(name, value, minValue, maxValue); err != nil {
		return 0, err
	}
	return value, nil
}

// NormalizeEcosystemAliasRoutePrefix validates an application-relative route
// prefix. A trailing slash is removed; all other non-canonical forms are
// rejected so configuration, route registration, and UI previews agree.
func NormalizeEcosystemAliasRoutePrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("ecosystem-alias route prefix must not be empty")
	}
	if prefix != strings.TrimSpace(prefix) || strings.IndexFunc(prefix, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("ecosystem-alias route prefix must not contain whitespace")
	}
	if !strings.HasPrefix(prefix, "/") {
		return "", fmt.Errorf("ecosystem-alias route prefix must start with /")
	}
	if strings.ContainsAny(prefix, "?#") {
		return "", fmt.Errorf("ecosystem-alias route prefix must not contain a query or fragment")
	}

	if strings.IndexFunc(prefix, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/-._~", r))
	}) >= 0 {
		return "", fmt.Errorf("ecosystem-alias route prefix must contain literal URL path characters")
	}
	normalized := strings.TrimRight(prefix, "/")
	if normalized == "" {
		return "", fmt.Errorf("ecosystem-alias route prefix must not be root")
	}
	if path.Clean(normalized) != normalized || strings.Contains(normalized, "//") {
		return "", fmt.Errorf("ecosystem-alias route prefix must be a canonical path")
	}
	return normalized, nil
}

// EcosystemAliasLookupPath builds the standard BRC-24 lookup path after
// validating both configured path components.
func EcosystemAliasLookupPath(basePath, routePrefix string) (string, error) {
	prefix, err := NormalizeEcosystemAliasRoutePrefix(routePrefix)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(basePath, "/")
	if base == "" {
		return prefix + "/overlay/lookup", nil
	}
	if !strings.HasPrefix(base, "/") || base != path.Clean(base) || strings.ContainsAny(base, "?#") || strings.IndexFunc(base, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("server base path must be a canonical slash-prefixed path")
	}
	return base + prefix + "/overlay/lookup", nil
}
