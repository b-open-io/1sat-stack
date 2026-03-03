package auth

import (
	"context"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/gofiber/fiber/v2"
)

type contextKey string

const identityContextKey contextKey = "auth.identity"
const apiKeyAuthContextKey contextKey = "auth.apikey"

// withIdentity stores the authenticated identity in the context.
func withIdentity(ctx context.Context, identity *ec.PublicKey) context.Context {
	return context.WithValue(ctx, identityContextKey, identity)
}

// GetIdentity returns the authenticated identity from a Fiber context.
// Returns nil if no identity is present (unauthenticated request that was allowed through).
func GetIdentity(c *fiber.Ctx) *ec.PublicKey {
	val := c.UserContext().Value(identityContextKey)
	if val == nil {
		return nil
	}
	identity, ok := val.(*ec.PublicKey)
	if !ok {
		return nil
	}
	return identity
}

// withApiKeyAuth stores the API key auth flag in the context.
func withApiKeyAuth(ctx context.Context) context.Context {
	return context.WithValue(ctx, apiKeyAuthContextKey, true)
}

// IsApiKeyAuth returns true if the request was authenticated via API key.
func IsApiKeyAuth(c *fiber.Ctx) bool {
	val := c.UserContext().Value(apiKeyAuthContextKey)
	if val == nil {
		return false
	}
	b, ok := val.(bool)
	return ok && b
}

// IsAuthenticated returns true if the Fiber context contains an authenticated identity
// or was authenticated via API key.
func IsAuthenticated(c *fiber.Ctx) bool {
	return GetIdentity(c) != nil || IsApiKeyAuth(c)
}
