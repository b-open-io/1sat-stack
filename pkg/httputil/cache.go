package httputil

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
)

// Confirmation thresholds — mirrored from pkg/txo/keys.go to avoid import cycles.
const (
	minCacheConfirmations = 2
	immutabilityBlocks    = 100
)

// SetConfirmationCache sets Cache-Control based on confirmation depth.
//
//	0-1 confirmations: no-store
//	2-99 confirmations: public, max-age=60
//	100+ confirmations: public, max-age=31536000, immutable
func SetConfirmationCache(c *fiber.Ctx, blockHeight uint32, tipHeight uint32) {
	if blockHeight == 0 || tipHeight == 0 || tipHeight < blockHeight {
		SetNoStore(c)
		return
	}

	confirmations := tipHeight - blockHeight + 1
	switch {
	case confirmations < minCacheConfirmations:
		SetNoStore(c)
	case confirmations < immutabilityBlocks:
		c.Set("Cache-Control", "public, max-age=60")
	default:
		SetImmutable(c)
	}
}

// SetImmutable marks the response as permanently cacheable.
func SetImmutable(c *fiber.Ctx) {
	c.Set("Cache-Control", "public, max-age=31536000, immutable")
}

// SetNoStore prevents any caching.
func SetNoStore(c *fiber.Ctx) {
	c.Set("Cache-Control", "no-store")
}

// SetPrivateNoStore prevents caching and marks as private (auth-gated routes).
func SetPrivateNoStore(c *fiber.Ctx) {
	c.Set("Cache-Control", "private, no-store")
}

// SetShortCache sets a short public cache TTL.
func SetShortCache(c *fiber.Ctx, seconds int) {
	c.Set("Cache-Control", fmt.Sprintf("public, max-age=%d", seconds))
}

// NoStoreMiddleware returns Fiber middleware that sets no-store on all responses.
func NoStoreMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		SetNoStore(c)
		return c.Next()
	}
}

// PrivateNoStoreMiddleware returns Fiber middleware that sets private, no-store on all responses.
func PrivateNoStoreMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		SetPrivateNoStore(c)
		return c.Next()
	}
}
