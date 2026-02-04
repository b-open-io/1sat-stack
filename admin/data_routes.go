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

	// ZSet operations
	zset := group.Group("/zset")
	zset.Get("/keys/:prefix", r.handleZSetKeys)
	zset.Get("/range/:key", r.handleZSetRange)
	zset.Get("/revrange/:key", r.handleZSetRevRange)
	zset.Get("/score/:key/:member", r.handleZSetScore)
	zset.Get("/card/:key", r.handleZSetCard)
	zset.Get("/sum/:key", r.handleZSetSum)

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

// KV handlers

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

// ZSet handlers

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
