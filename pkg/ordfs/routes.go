package ordfs

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Routes handles HTTP routes for content serving
type Routes struct {
	content *ContentService
	logger  *slog.Logger
}

// RoutesDeps holds dependencies for routes
type RoutesDeps struct {
	Content *ContentService
	Logger  *slog.Logger
}

// NewRoutes creates a new routes handler
func NewRoutes(deps *RoutesDeps) *Routes {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		content: deps.Content,
		logger:  logger,
	}
}

// Register registers the routes on a Fiber router
func (r *Routes) Register(router fiber.Router, prefix string) {
	g := router.Group(prefix)

	// Metadata endpoint - returns metadata without content bytes
	g.Get("/metadata/*", r.HandleMetadata)

	// Output endpoint - returns raw output bytes
	g.Get("/output/*", r.HandleOutput)

	// Preview endpoints - render HTML content
	g.Get("/preview/:b64HtmlData", r.HandlePreview)
	g.Post("/preview", r.HandlePreviewPost)
}

// RegisterContent registers just the content endpoint at the given prefix
// This allows serving content at a root-level path like /content
func (r *Routes) RegisterContent(router fiber.Router, prefix string) {
	g := router.Group(prefix)
	g.Get("/*", r.HandleContent)
}

// HandleContent serves inscription content
// @Summary Get inscription content
// @Description Serve the content of an inscription by outpoint or txid
// @Tags ordfs
// @Produce octet-stream
// @Param path path string true "Outpoint (txid_vout) or txid"
// @Success 200 {file} binary "Content"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Not found"
// @Router /content/{path} [get]
func (r *Routes) HandleContent(c *fiber.Ctx) error {
	path := c.Params("*")
	if path == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "path is required",
		})
	}

	req, err := parseContentPath(path)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	req.Content = true

	resp, err := r.content.Load(c.Context(), req)
	if err != nil {
		r.logger.Debug("failed to load content", "path", path, "error", err)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if resp.Content == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "no content found",
		})
	}

	c.Set("Content-Type", resp.ContentType)
	c.Set("Content-Length", strconv.Itoa(len(resp.Content)))

	if resp.Outpoint != nil {
		c.Set("X-Outpoint", resp.Outpoint.String())
	}
	if resp.Origin != nil {
		c.Set("X-Origin", resp.Origin.String())
	}

	return c.Send(resp.Content)
}

// HandleMetadata returns content metadata without the content bytes
// @Summary Get content metadata
// @Description Get metadata about inscription content without downloading the content
// @Tags ordfs
// @Produce json
// @Param path path string true "Outpoint (txid_vout) or txid"
// @Success 200 {object} Response "Metadata"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Not found"
// @Router /ordfs/metadata/{path} [get]
func (r *Routes) HandleMetadata(c *fiber.Ctx) error {
	path := c.Params("*")
	if path == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "path is required",
		})
	}

	req, err := parseContentPath(path)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	req.Content = true // Parse content to get metadata
	req.Map = true

	resp, err := r.content.Load(c.Context(), req)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Return metadata without content bytes
	return c.JSON(fiber.Map{
		"outpoint":      resp.Outpoint,
		"origin":        resp.Origin,
		"contentType":   resp.ContentType,
		"contentLength": resp.ContentLength,
		"map":           resp.Map,
		"sequence":      resp.Sequence,
		"parent":        resp.Parent,
	})
}

// HandleOutput returns raw output bytes
// @Summary Get raw output
// @Description Get the raw transaction output bytes
// @Tags ordfs
// @Produce octet-stream
// @Param path path string true "Outpoint (txid_vout) or txid"
// @Success 200 {file} binary "Output bytes"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Not found"
// @Router /ordfs/output/{path} [get]
func (r *Routes) HandleOutput(c *fiber.Ctx) error {
	path := c.Params("*")
	if path == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "path is required",
		})
	}

	req, err := parseContentPath(path)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	req.Output = true

	resp, err := r.content.Load(c.Context(), req)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if resp.Output == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "no output found",
		})
	}

	c.Set("Content-Type", "application/octet-stream")
	return c.Send(resp.Output)
}

// HandlePreview renders base64-encoded HTML content
// @Summary Preview HTML content
// @Description Decode and render base64-encoded HTML
// @Tags ordfs
// @Produce html
// @Param b64HtmlData path string true "Base64-encoded HTML content"
// @Success 200 {string} string "HTML content"
// @Failure 400 {object} map[string]string "Bad request"
// @Router /ordfs/preview/{b64HtmlData} [get]
func (r *Routes) HandlePreview(c *fiber.Ctx) error {
	b64Html := c.Params("b64HtmlData")
	if b64Html == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "missing base64 HTML data",
		})
	}

	htmlBytes, err := base64.StdEncoding.DecodeString(b64Html)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid base64 data",
		})
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Send(htmlBytes)
}

// HandlePreviewPost echoes back the request body with its content type
// @Summary Preview posted content
// @Description Echo back the request body for preview rendering
// @Tags ordfs
// @Accept */*
// @Produce */*
// @Success 200 {string} string "Content"
// @Failure 400 {object} map[string]string "Bad request"
// @Router /ordfs/preview [post]
func (r *Routes) HandlePreviewPost(c *fiber.Ctx) error {
	body := c.Body()
	if len(body) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "missing request body",
		})
	}

	contentType := c.Get("Content-Type")
	if contentType != "" {
		c.Set("Content-Type", contentType)
	}
	return c.Send(body)
}

// Regex patterns for path parsing
var (
	// Matches txid_vout (outpoint format)
	outpointPattern = regexp.MustCompile(`^([a-fA-F0-9]{64})_(\d+)$`)
	// Matches txid:seq (sequence format)
	seqPattern = regexp.MustCompile(`^([a-fA-F0-9]{64}):(-?\d+)$`)
	// Matches just txid
	txidPattern = regexp.MustCompile(`^([a-fA-F0-9]{64})$`)
)

// parseContentPath parses a content path into a Request
func parseContentPath(path string) (*Request, error) {
	// Remove any leading/trailing slashes
	path = strings.Trim(path, "/")

	// Split on slash to handle file paths
	parts := strings.SplitN(path, "/", 2)
	pointer := parts[0]

	req := &Request{}

	// Try outpoint format (txid_vout)
	if matches := outpointPattern.FindStringSubmatch(pointer); matches != nil {
		txid, err := chainhash.NewHashFromHex(matches[1])
		if err != nil {
			return nil, fmt.Errorf("invalid txid: %w", err)
		}
		vout, err := strconv.ParseUint(matches[2], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid vout: %w", err)
		}
		req.Outpoint = &transaction.Outpoint{
			Txid:  *txid,
			Index: uint32(vout),
		}
		return req, nil
	}

	// Try sequence format (txid:seq)
	if matches := seqPattern.FindStringSubmatch(pointer); matches != nil {
		txid, err := chainhash.NewHashFromHex(matches[1])
		if err != nil {
			return nil, fmt.Errorf("invalid txid: %w", err)
		}
		seq, err := strconv.Atoi(matches[2])
		if err != nil {
			return nil, fmt.Errorf("invalid sequence: %w", err)
		}
		// For sequence format, we use txid_0 as starting point
		req.Outpoint = &transaction.Outpoint{
			Txid:  *txid,
			Index: 0,
		}
		req.Seq = &seq
		return req, nil
	}

	// Try just txid
	if matches := txidPattern.FindStringSubmatch(pointer); matches != nil {
		txid, err := chainhash.NewHashFromHex(matches[1])
		if err != nil {
			return nil, fmt.Errorf("invalid txid: %w", err)
		}
		req.Txid = txid
		return req, nil
	}

	return nil, fmt.Errorf("invalid path format: expected txid, txid_vout, or txid:seq")
}
