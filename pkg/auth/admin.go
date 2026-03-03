package auth

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/gofiber/fiber/v2"
)

// KeyAdminPubkeys is the store key for the set of admin identity public keys.
var KeyAdminPubkeys = []byte("s:admin:pubkeys")

// AdminGuard returns a Fiber middleware that restricts access to admin endpoints.
// It checks the BRC-103/104 identity against the admin pubkey set in the store.
func AdminGuard(s store.Store, logger *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		identity := GetIdentity(c)
		if identity == nil {
			logger.Warn("unauthorized admin access attempt: no identity",
				"path", c.Path(),
				"method", c.Method(),
				"ip", c.IP())
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "unauthorized",
			})
		}

		pubkey := []byte(identity.ToDERHex())
		ctx := context.Background()

		isMember, err := s.SIsMember(ctx, KeyAdminPubkeys, pubkey)
		if err != nil {
			logger.Error("failed to check admin membership", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal error",
			})
		}
		if isMember {
			return c.Next()
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

// IsSetup checks whether any admin identities have been configured.
func IsSetup(ctx context.Context, s store.Store) (bool, error) {
	members, err := s.SMembers(ctx, KeyAdminPubkeys)
	if err != nil {
		return false, err
	}
	return len(members) > 0, nil
}
