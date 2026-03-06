package auth

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/gofiber/fiber/v2"
)

// KeyAdminUsers is the store key for the hash of admin users.
// Field: pubkey (string), Value: JSON-encoded AdminUser.
var KeyAdminUsers = []byte("admin:users")

// KeyAdminRequests is the store key for the hash of pending access requests.
// Field: pubkey (string), Value: JSON-encoded AccessRequest.
var KeyAdminRequests = []byte("admin:requests")

// AdminUser represents a user entry in the admin users hash.
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
// It checks the BRC-103/104 identity against the admin users hash in the store,
// or allows through if authenticated via API key.
func AdminGuard(s store.Store, logger *slog.Logger) fiber.Handler {
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
		user, err := GetAdminUser(c.Context(), s, pubkey)
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
func IsSetup(ctx context.Context, s store.Store) (bool, error) {
	entries, err := s.HGetAll(ctx, KeyAdminUsers)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

// GetAdminUser retrieves a single admin user by pubkey.
func GetAdminUser(ctx context.Context, s store.Store, pubkey string) (*AdminUser, error) {
	data, err := s.HGet(ctx, KeyAdminUsers, []byte(pubkey))
	if err != nil {
		return nil, nil
	}
	if data == nil {
		return nil, nil
	}
	var user AdminUser
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// ListAdminUsers returns all admin users.
func ListAdminUsers(ctx context.Context, s store.Store) ([]AdminUser, error) {
	entries, err := s.HGetAll(ctx, KeyAdminUsers)
	if err != nil {
		return nil, err
	}
	users := make([]AdminUser, 0, len(entries))
	for _, data := range entries {
		var user AdminUser
		if err := json.Unmarshal(data, &user); err != nil {
			continue
		}
		users = append(users, user)
	}
	return users, nil
}

// SaveAdminUser saves an admin user to the hash.
func SaveAdminUser(ctx context.Context, s store.Store, user AdminUser) error {
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	return s.HSet(ctx, KeyAdminUsers, []byte(user.Pubkey), data)
}

// DeleteAdminUser removes an admin user from the hash.
func DeleteAdminUser(ctx context.Context, s store.Store, pubkey string) error {
	return s.HDel(ctx, KeyAdminUsers, []byte(pubkey))
}

// SaveAccessRequest saves a pending access request.
func SaveAccessRequest(ctx context.Context, s store.Store, req AccessRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return s.HSet(ctx, KeyAdminRequests, []byte(req.Pubkey), data)
}

// ListAccessRequests returns all pending access requests.
func ListAccessRequests(ctx context.Context, s store.Store) ([]AccessRequest, error) {
	entries, err := s.HGetAll(ctx, KeyAdminRequests)
	if err != nil {
		return nil, err
	}
	requests := make([]AccessRequest, 0, len(entries))
	for _, data := range entries {
		var req AccessRequest
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		requests = append(requests, req)
	}
	return requests, nil
}

// DeleteAccessRequest removes a pending access request.
func DeleteAccessRequest(ctx context.Context, s store.Store, pubkey string) error {
	return s.HDel(ctx, KeyAdminRequests, []byte(pubkey))
}
