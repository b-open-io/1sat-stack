package auth

import (
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// AdminGuard returns a Fiber middleware that restricts access to admin endpoints.
// It checks two authorization methods in order:
//  1. BRC-103/104 identity — if the request has an authenticated identity
//     (set by the global auth middleware) and that identity's public key
//     is in the adminPubkeys allowlist, access is granted.
//  2. Bearer token — if a bearerToken is configured and the request's
//     Authorization header matches, access is granted.
//
// If neither check passes, the request gets a 401.
func AdminGuard(adminPubkeys []string, bearerToken string, logger *slog.Logger) fiber.Handler {
	// Build a set for O(1) lookup
	allowed := make(map[string]struct{}, len(adminPubkeys))
	for _, pk := range adminPubkeys {
		allowed[pk] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		// Check BRC-103/104 identity
		if identity := GetIdentity(c); identity != nil {
			if _, ok := allowed[identity.ToDERHex()]; ok {
				return c.Next()
			}
		}

		// Fall back to bearer token
		if bearerToken != "" {
			if strings.TrimPrefix(c.Get("Authorization"), "Bearer ") == bearerToken {
				return c.Next()
			}
		}

		logger.Warn("unauthorized admin access attempt",
			"path", c.Path(),
			"method", c.Method(),
			"ip", c.IP())
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "unauthorized",
		})
	}
}
