package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/gofiber/fiber/v2"
)

const (
	prefixUser    = "user:"
	prefixRequest = "request:"
)

// AdminUser represents a user entry.
type AdminUser struct {
	Pubkey string `json:"pubkey"`
	Name   string `json:"name"`
	Admin  bool   `json:"admin"`
}

// AccessRequest represents a pending access request.
type AccessRequest struct {
	Pubkey string `json:"pubkey"`
	Name   string `json:"name"`
}

// AdminGuard returns a Fiber middleware that restricts access to admin endpoints.
func AdminGuard(cs config.Store, logger *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if IsApiKeyAuth(c) {
			return c.Next()
		}

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

		pubkey := identity.ToDERHex()
		user, err := GetAdminUser(c.Context(), cs, pubkey)
		if err != nil {
			logger.Error("failed to check admin membership", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal error",
			})
		}
		if user != nil && user.Admin {
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

// IsSetup checks whether any admin users have been configured.
func IsSetup(ctx context.Context, cs config.Store) (bool, error) {
	entries, err := cs.List(ctx, prefixUser)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

// GetAdminUser retrieves a single admin user by pubkey.
func GetAdminUser(ctx context.Context, cs config.Store, pubkey string) (*AdminUser, error) {
	data, err := cs.Get(ctx, prefixUser+pubkey)
	if errors.Is(err, config.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var user AdminUser
	if err := json.Unmarshal([]byte(data), &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// ListAdminUsers returns all admin users.
func ListAdminUsers(ctx context.Context, cs config.Store) ([]AdminUser, error) {
	entries, err := cs.List(ctx, prefixUser)
	if err != nil {
		return nil, err
	}
	users := make([]AdminUser, 0, len(entries))
	for _, data := range entries {
		var user AdminUser
		if err := json.Unmarshal([]byte(data), &user); err != nil {
			continue
		}
		users = append(users, user)
	}
	return users, nil
}

// SaveAdminUser saves an admin user.
func SaveAdminUser(ctx context.Context, cs config.Store, user AdminUser) error {
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	return cs.Set(ctx, prefixUser+user.Pubkey, string(data))
}

// DeleteAdminUser removes an admin user.
func DeleteAdminUser(ctx context.Context, cs config.Store, pubkey string) error {
	return cs.Delete(ctx, prefixUser+pubkey)
}

// SaveAccessRequest saves a pending access request.
func SaveAccessRequest(ctx context.Context, cs config.Store, req AccessRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return cs.Set(ctx, prefixRequest+req.Pubkey, string(data))
}

// ListAccessRequests returns all pending access requests.
func ListAccessRequests(ctx context.Context, cs config.Store) ([]AccessRequest, error) {
	entries, err := cs.List(ctx, prefixRequest)
	if err != nil {
		return nil, err
	}
	requests := make([]AccessRequest, 0, len(entries))
	for _, data := range entries {
		var req AccessRequest
		if err := json.Unmarshal([]byte(data), &req); err != nil {
			continue
		}
		requests = append(requests, req)
	}
	return requests, nil
}

// DeleteAccessRequest removes a pending access request.
func DeleteAccessRequest(ctx context.Context, cs config.Store, pubkey string) error {
	return cs.Delete(ctx, prefixRequest+pubkey)
}
