package admin

import (
	"encoding/hex"
	"log/slog"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// DataRoutes handles generic data query routes
type DataRoutes struct {
	store  store.Store
	logger *slog.Logger
}

// NewDataRoutes creates a new DataRoutes instance
func NewDataRoutes(s store.Store, logger *slog.Logger) *DataRoutes {
	return &DataRoutes{
		store:  s,
		logger: logger,
	}
}

// Register registers data routes on a Fiber app group
func (r *DataRoutes) Register(group fiber.Router) {
	// KV operations
	kv := group.Group("/kv")
	kv.Get("/get/:key", r.handleKVGet)

	// Set operations
	set := group.Group("/set")
	set.Get("/members/:key", r.handleSetMembers)
	set.Get("/ismember/:key/:member", r.handleSetIsMember)

	// Hash operations
	hash := group.Group("/hash")
	hash.Get("/getall/:key", r.handleHashGetAll)
	hash.Get("/get/:key/:field", r.handleHashGet)
	hash.Post("/mget/:key", r.handleHashMGet)
	hash.Post("/del/:key", r.handleHashDel)

	// ZSet operations
	zset := group.Group("/zset")
	zset.Get("/keys/:prefix", r.handleZSetKeys)
	zset.Get("/range/:key", r.handleZSetRange)
	zset.Get("/revrange/:key", r.handleZSetRevRange)
	zset.Get("/score/:key/:member", r.handleZSetScore)
	zset.Get("/card/:key", r.handleZSetCard)
	zset.Get("/sum/:key", r.handleZSetSum)
	zset.Delete("/rem/:key/:member", r.handleZSetRem)

	// Key operations
	group.Delete("/key/*", r.handleDeleteKey)

	// Search
	group.Post("/search", r.handleSearch)

	r.logger.Debug("registered data routes")
}

// renderValue converts a byte slice to a human-readable string
func renderValue(b []byte) string {
	switch len(b) {
	case 32:
		// chainhash.Hash
		hash, err := chainhash.NewHash(b)
		if err == nil {
			return hash.String()
		}
		return hex.EncodeToString(b)
	case 36:
		// transaction.Outpoint
		op := transaction.NewOutpointFromBytes(b)
		if op != nil {
			return op.String()
		}
		return hex.EncodeToString(b)
	default:
		// Try as string first, fall back to hex
		if isPrintable(b) {
			return string(b)
		}
		return hex.EncodeToString(b)
	}
}

// isPrintable checks if a byte slice is printable ASCII
func isPrintable(b []byte) bool {
	for _, c := range b {
		if c < 32 || c > 126 {
			return false
		}
	}
	return len(b) > 0
}

// Key handlers

// handleDeleteKey deletes a storage key, draining sorted-set members first.
// @Summary Delete key
// @Description Deletes a storage key; sorted-set members are drained first, then the KV key is removed
// @Tags admin
// @Produce json
// @Param key path string true "Storage key"
// @Success 200 {object} map[string]interface{} "key, removed member count, deleted flag"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/key/{key} [delete]
func (r *DataRoutes) handleDeleteKey(c *fiber.Ctx) error {
	key := c.Params("*")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	keyBytes := []byte(key)

	// Try deleting as a sorted set first (drain all members)
	removed := int64(0)
	for {
		members, err := r.store.ZRange(c.Context(), keyBytes, store.ScoreRange{Count: 500})
		if err != nil || len(members) == 0 {
			break
		}
		batch := make([][]byte, len(members))
		for i, m := range members {
			batch[i] = m.Member
		}
		if err := r.store.ZRem(c.Context(), keyBytes, batch...); err != nil {
			r.logger.Error("failed to remove zset members", "key", key, "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to remove members",
			})
		}
		removed += int64(len(batch))
	}

	// Also delete the KV key in case it's a simple key
	r.store.Del(c.Context(), keyBytes)

	r.logger.Info("deleted key", "key", key, "removed", removed)
	return c.JSON(fiber.Map{
		"key":     key,
		"removed": removed,
		"deleted": true,
	})
}

// KV handlers

// handleKVGet returns the value stored at a KV key.
// @Summary Get KV value
// @Description Returns the value stored at a key, rendered as txid, outpoint, string, or hex
// @Tags admin
// @Produce json
// @Param key path string true "Storage key"
// @Success 200 {object} map[string]string "key and value"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Key not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/kv/get/{key} [get]
func (r *DataRoutes) handleKVGet(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	value, err := r.store.Get(c.Context(), []byte(key))
	if err != nil {
		r.logger.Error("failed to get kv", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get value",
		})
	}

	if value == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "key not found",
		})
	}

	return c.JSON(fiber.Map{
		"key":   key,
		"value": renderValue(value),
	})
}

// Set handlers

// handleSetMembers returns all members of a set.
// @Summary Get set members
// @Description Returns all members of a set
// @Tags admin
// @Produce json
// @Param key path string true "Set key"
// @Success 200 {object} map[string]interface{} "key, count, and members"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/set/members/{key} [get]
func (r *DataRoutes) handleSetMembers(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	members, err := r.store.SMembers(c.Context(), []byte(key))
	if err != nil {
		r.logger.Error("failed to get set members", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get members",
		})
	}

	rendered := make([]string, len(members))
	for i, m := range members {
		rendered[i] = renderValue(m)
	}

	return c.JSON(fiber.Map{
		"key":     key,
		"count":   len(rendered),
		"members": rendered,
	})
}

// handleSetIsMember checks set membership.
// @Summary Check set membership
// @Description Checks whether a member is in a set
// @Tags admin
// @Produce json
// @Param key path string true "Set key"
// @Param member path string true "Member value"
// @Success 200 {object} map[string]interface{} "key, member, and is_member flag"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/set/ismember/{key}/{member} [get]
func (r *DataRoutes) handleSetIsMember(c *fiber.Ctx) error {
	key := c.Params("key")
	member := c.Params("member")
	if key == "" || member == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key and member are required",
		})
	}

	memberBytes := []byte(member)
	isMember, err := r.store.SIsMember(c.Context(), []byte(key), memberBytes)
	if err != nil {
		r.logger.Error("failed to check set membership", "key", key, "member", member, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to check membership",
		})
	}

	return c.JSON(fiber.Map{
		"key":       key,
		"member":    renderValue(memberBytes),
		"is_member": isMember,
	})
}

// Hash handlers

// handleHashGetAll returns all fields of a hash.
// @Summary Get all hash fields
// @Description Returns all fields and values of a hash
// @Tags admin
// @Produce json
// @Param key path string true "Hash key"
// @Success 200 {object} map[string]interface{} "key, count, and fields"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/hash/getall/{key} [get]
func (r *DataRoutes) handleHashGetAll(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	fields, err := r.store.HGetAll(c.Context(), []byte(key))
	if err != nil {
		r.logger.Error("failed to get hash fields", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get fields",
		})
	}

	rendered := make(map[string]string)
	for field, value := range fields {
		rendered[field] = renderValue(value)
	}

	return c.JSON(fiber.Map{
		"key":    key,
		"count":  len(rendered),
		"fields": rendered,
	})
}

// handleHashGet returns a single hash field.
// @Summary Get hash field
// @Description Returns the value of a single hash field
// @Tags admin
// @Produce json
// @Param key path string true "Hash key"
// @Param field path string true "Field name"
// @Success 200 {object} map[string]string "key, field, and value"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Field not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/hash/get/{key}/{field} [get]
func (r *DataRoutes) handleHashGet(c *fiber.Ctx) error {
	key := c.Params("key")
	field := c.Params("field")
	if key == "" || field == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key and field are required",
		})
	}

	value, err := r.store.HGet(c.Context(), []byte(key), []byte(field))
	if err != nil {
		r.logger.Error("failed to get hash field", "key", key, "field", field, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get field",
		})
	}

	if value == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "field not found",
		})
	}

	return c.JSON(fiber.Map{
		"key":   key,
		"field": field,
		"value": renderValue(value),
	})
}

// handleHashMGet returns multiple hash fields.
// @Summary Get multiple hash fields
// @Description Returns the values of the requested hash fields
// @Tags admin
// @Accept json
// @Produce json
// @Param key path string true "Hash key"
// @Param body body []string true "Field names"
// @Success 200 {object} map[string]interface{} "key, count, and fields"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/hash/mget/{key} [post]
func (r *DataRoutes) handleHashMGet(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	var fields []string
	if err := c.BodyParser(&fields); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body, expected array of field names",
		})
	}

	if len(fields) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "at least one field is required",
		})
	}

	fieldBytes := make([][]byte, len(fields))
	for i, f := range fields {
		fieldBytes[i] = []byte(f)
	}

	values, err := r.store.HMGet(c.Context(), []byte(key), fieldBytes...)
	if err != nil {
		r.logger.Error("failed to get hash fields", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get fields",
		})
	}

	rendered := make(map[string]string)
	for i, value := range values {
		if value != nil {
			rendered[fields[i]] = renderValue(value)
		}
	}

	return c.JSON(fiber.Map{
		"key":    key,
		"count":  len(rendered),
		"fields": rendered,
	})
}

// handleHashDel deletes one or more fields from a hash. Fields must be
// supplied as a JSON array of hex-encoded byte sequences; this lets callers
// delete fields whose underlying bytes are binary (e.g. outpoint bytes in the
// "spnd" hash) without URL-encoding pitfalls.
// @Summary Delete hash fields
// @Description Deletes one or more hash fields; fields are supplied as hex-encoded byte sequences
// @Tags admin
// @Accept json
// @Produce json
// @Param key path string true "Hash key"
// @Param body body []string true "Hex-encoded field bytes"
// @Success 200 {object} map[string]interface{} "key and deleted count"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/hash/del/{key} [post]
func (r *DataRoutes) handleHashDel(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	var fields []string
	if err := c.BodyParser(&fields); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body, expected JSON array of hex-encoded field bytes",
		})
	}
	if len(fields) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "at least one field is required",
		})
	}

	fieldBytes := make([][]byte, len(fields))
	for i, f := range fields {
		b, err := hex.DecodeString(f)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid hex in field at index " + strconv.Itoa(i),
			})
		}
		fieldBytes[i] = b
	}

	if err := r.store.HDel(c.Context(), []byte(key), fieldBytes...); err != nil {
		r.logger.Error("failed to delete hash fields", "key", key, "count", len(fields), "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete fields",
		})
	}

	return c.JSON(fiber.Map{
		"key":     key,
		"deleted": len(fieldBytes),
	})
}

// ZSet handlers

// handleZSetRange returns sorted-set members in ascending score order.
// @Summary Get sorted set range
// @Description Returns sorted-set members with scores in ascending order
// @Tags admin
// @Produce json
// @Param key path string true "Sorted set key"
// @Param min query number false "Minimum score"
// @Param max query number false "Maximum score"
// @Param offset query int false "Pagination offset"
// @Param count query int false "Max results (default 25)"
// @Success 200 {object} map[string]interface{} "key, count, and items"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/zset/range/{key} [get]
func (r *DataRoutes) handleZSetRange(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	scoreRange := r.parseScoreRange(c)

	members, err := r.store.ZRange(c.Context(), []byte(key), scoreRange)
	if err != nil {
		r.logger.Error("failed to get zset range", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get range",
		})
	}

	count, _ := r.store.ZCard(c.Context(), []byte(key))

	items := make([]fiber.Map, len(members))
	for i, m := range members {
		items[i] = fiber.Map{
			"value": renderValue(m.Member),
			"score": m.Score,
		}
	}

	return c.JSON(fiber.Map{
		"key":   key,
		"count": count,
		"items": items,
	})
}

// handleZSetRevRange returns sorted-set members in descending score order.
// @Summary Get sorted set reverse range
// @Description Returns sorted-set members with scores in descending order
// @Tags admin
// @Produce json
// @Param key path string true "Sorted set key"
// @Param min query number false "Minimum score"
// @Param max query number false "Maximum score"
// @Param offset query int false "Pagination offset"
// @Param count query int false "Max results (default 25)"
// @Success 200 {object} map[string]interface{} "key, count, and items"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/zset/revrange/{key} [get]
func (r *DataRoutes) handleZSetRevRange(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	scoreRange := r.parseScoreRange(c)

	members, err := r.store.ZRevRange(c.Context(), []byte(key), scoreRange)
	if err != nil {
		r.logger.Error("failed to get zset revrange", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get range",
		})
	}

	count, _ := r.store.ZCard(c.Context(), []byte(key))

	items := make([]fiber.Map, len(members))
	for i, m := range members {
		items[i] = fiber.Map{
			"value": renderValue(m.Member),
			"score": m.Score,
		}
	}

	return c.JSON(fiber.Map{
		"key":   key,
		"count": count,
		"items": items,
	})
}

// handleZSetScore returns the score of a sorted-set member.
// @Summary Get sorted set member score
// @Description Returns the score of a member, or exists=false if not present
// @Tags admin
// @Produce json
// @Param key path string true "Sorted set key"
// @Param member path string true "Member value"
// @Success 200 {object} map[string]interface{} "key, member, score, and exists flag"
// @Failure 400 {object} map[string]string "Bad request"
// @Security BearerAuth
// @Router /api/data/zset/score/{key}/{member} [get]
func (r *DataRoutes) handleZSetScore(c *fiber.Ctx) error {
	key := c.Params("key")
	member := c.Params("member")
	if key == "" || member == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key and member are required",
		})
	}

	memberBytes := []byte(member)
	score, err := r.store.ZScore(c.Context(), []byte(key), memberBytes)
	if err != nil {
		// ZScore returns error if member doesn't exist
		return c.JSON(fiber.Map{
			"key":    key,
			"member": renderValue(memberBytes),
			"exists": false,
		})
	}

	return c.JSON(fiber.Map{
		"key":    key,
		"member": renderValue(memberBytes),
		"score":  score,
		"exists": true,
	})
}

// handleZSetRem removes a member from a sorted set.
// @Summary Remove sorted set member
// @Description Removes a member from a sorted set; member is parsed as txid, hex, or string
// @Tags admin
// @Produce json
// @Param key path string true "Sorted set key"
// @Param member path string true "Member value (txid hex, raw hex, or string)"
// @Success 200 {object} map[string]interface{} "key, member, and removed flag"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/zset/rem/{key}/{member} [delete]
func (r *DataRoutes) handleZSetRem(c *fiber.Ctx) error {
	key := c.Params("key")
	member := c.Params("member")
	if key == "" || member == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key and member are required",
		})
	}

	// Try as txid (64-char hex, byte-reversed) first, then raw hex, then string
	var memberBytes []byte
	if len(member) == 64 {
		if h, err := chainhash.NewHashFromHex(member); err == nil {
			memberBytes = h[:]
		}
	}
	if memberBytes == nil {
		if b, err := hex.DecodeString(member); err == nil {
			memberBytes = b
		} else {
			memberBytes = []byte(member)
		}
	}

	if err := r.store.ZRem(c.Context(), []byte(key), memberBytes); err != nil {
		r.logger.Error("failed to remove member", "key", key, "member", member, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to remove member",
		})
	}

	return c.JSON(fiber.Map{
		"key":     key,
		"member":  renderValue(memberBytes),
		"removed": true,
	})
}

// handleZSetCard returns the member count of a sorted set.
// @Summary Get sorted set cardinality
// @Description Returns the number of members in a sorted set
// @Tags admin
// @Produce json
// @Param key path string true "Sorted set key"
// @Success 200 {object} map[string]interface{} "key and count"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/zset/card/{key} [get]
func (r *DataRoutes) handleZSetCard(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	count, err := r.store.ZCard(c.Context(), []byte(key))
	if err != nil {
		r.logger.Error("failed to get zset card", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get count",
		})
	}

	return c.JSON(fiber.Map{
		"key":   key,
		"count": count,
	})
}

// handleZSetSum returns the sum of scores in a sorted set.
// @Summary Get sorted set score sum
// @Description Returns the sum of all member scores in a sorted set
// @Tags admin
// @Produce json
// @Param key path string true "Sorted set key"
// @Success 200 {object} map[string]interface{} "key and sum"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/zset/sum/{key} [get]
func (r *DataRoutes) handleZSetSum(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key is required",
		})
	}

	sum, err := r.store.ZSum(c.Context(), []byte(key))
	if err != nil {
		r.logger.Error("failed to get zset sum", "key", key, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get sum",
		})
	}

	return c.JSON(fiber.Map{
		"key": key,
		"sum": sum,
	})
}

// handleZSetKeys lists sorted-set keys matching a prefix.
// @Summary List sorted set keys
// @Description Returns sorted-set keys matching a prefix
// @Tags admin
// @Produce json
// @Param prefix path string true "Key prefix"
// @Success 200 {object} map[string]interface{} "prefix, count, and keys"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/zset/keys/{prefix} [get]
func (r *DataRoutes) handleZSetKeys(c *fiber.Ctx) error {
	prefix := c.Params("prefix")

	keys, err := r.store.ZKeys(c.Context(), []byte(prefix))
	if err != nil {
		r.logger.Error("failed to get zset keys", "prefix", prefix, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get keys",
		})
	}

	if keys == nil {
		keys = []string{}
	}

	return c.JSON(fiber.Map{
		"prefix": prefix,
		"count":  len(keys),
		"keys":   keys,
	})
}

// Search handler

// handleSearch queries members across multiple sorted-set keys.
// @Summary Search sorted sets
// @Description Queries members across multiple sorted-set keys with union, intersect, or difference join
// @Tags admin
// @Accept json
// @Produce json
// @Param body body object{keys=[]string,from=number,to=number,limit=int,join=string,reverse=bool} true "Search query"
// @Success 200 {object} map[string]interface{} "count and results"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Security BearerAuth
// @Router /api/data/search [post]
func (r *DataRoutes) handleSearch(c *fiber.Ctx) error {
	var req struct {
		Keys    []string `json:"keys"`
		From    *float64 `json:"from"`
		To      *float64 `json:"to"`
		Limit   uint32   `json:"limit"`
		Join    string   `json:"join"`
		Reverse bool     `json:"reverse"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if len(req.Keys) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "at least one key is required",
		})
	}

	// Convert keys to bytes
	keyBytes := make([][]byte, len(req.Keys))
	for i, k := range req.Keys {
		keyBytes[i] = []byte(k)
	}

	// Parse join type
	joinType := store.JoinUnion
	switch req.Join {
	case "union", "":
		joinType = store.JoinUnion
	case "intersect":
		joinType = store.JoinIntersect
	case "difference":
		joinType = store.JoinDifference
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid join type, must be union, intersect, or difference",
		})
	}

	// Default limit
	if req.Limit == 0 {
		req.Limit = 25
	}

	cfg := &store.SearchCfg{
		Keys:     keyBytes,
		From:     req.From,
		To:       req.To,
		Limit:    req.Limit,
		JoinType: joinType,
		Reverse:  req.Reverse,
	}

	members, err := r.store.Search(c.Context(), cfg)
	if err != nil {
		r.logger.Error("failed to search", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to search",
		})
	}

	results := make([]fiber.Map, len(members))
	for i, m := range members {
		results[i] = fiber.Map{
			"value": renderValue(m.Member),
			"score": m.Score,
			"key":   string(m.Key),
		}
	}

	return c.JSON(fiber.Map{
		"count":   len(results),
		"results": results,
	})
}

// parseScoreRange parses ScoreRange from query params
func (r *DataRoutes) parseScoreRange(c *fiber.Ctx) store.ScoreRange {
	sr := store.ScoreRange{
		Count: 25, // default
	}

	if minStr := c.Query("min"); minStr != "" {
		if min, err := strconv.ParseFloat(minStr, 64); err == nil {
			sr.Min = &min
		}
	}

	if maxStr := c.Query("max"); maxStr != "" {
		if max, err := strconv.ParseFloat(maxStr, 64); err == nil {
			sr.Max = &max
		}
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		if offset, err := strconv.ParseInt(offsetStr, 10, 64); err == nil {
			sr.Offset = offset
		}
	}

	if countStr := c.Query("count"); countStr != "" {
		if count, err := strconv.ParseInt(countStr, 10, 64); err == nil {
			sr.Count = count
		}
	}

	return sr
}
