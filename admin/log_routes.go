package admin

import (
	"strconv"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/gofiber/fiber/v2"
)

// handleQueryLogs queries persistent logs with filters.
// @Summary Query logs
// @Description Query system logs with optional filtering by level, component, time range
// @Tags admin
// @Produce json
// @Param component query string false "Component filter"
// @Param level query string false "Log level filter (DEBUG, INFO, WARN, ERROR)"
// @Param since query string false "Start time (RFC3339)"
// @Param until query string false "End time (RFC3339)"
// @Param search query string false "Text search in message"
// @Param limit query int false "Results per page (default 100, max 1000)"
// @Param offset query int false "Pagination offset"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/logs [get]
func (r *Routes) handleQueryLogs(c *fiber.Ctx) error {
	if r.logStore == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "log store not available",
		})
	}

	q := logging.LogQuery{
		Component: c.Query("component"),
		Level:     c.Query("level"),
		Search:    c.Query("search"),
		Limit:     100,
	}

	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "limit must be 1-1000"})
		}
		q.Limit = n
	}
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid offset"})
		}
		q.Offset = n
	}
	if v := c.Query("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid since time"})
		}
		q.Since = t
	}
	if v := c.Query("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid until time"})
		}
		q.Until = t
	}

	entries, total, err := r.logStore.Query(q)
	if err != nil {
		r.logger.Error("log query failed", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
	}

	return c.JSON(fiber.Map{
		"total":   total,
		"entries": entries,
	})
}
