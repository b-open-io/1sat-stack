package ordfs

import (
	"encoding/base64"
	"encoding/json"
	"errors"
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
	ordfs  *Ordfs
	logger *slog.Logger
}

// RoutesDeps holds dependencies for routes
type RoutesDeps struct {
	Ordfs  *Ordfs
	Logger *slog.Logger
}

// NewRoutes creates a new routes handler
func NewRoutes(deps *RoutesDeps) *Routes {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Routes{
		ordfs:  deps.Ordfs,
		logger: logger,
	}
}

// Register registers the routes on a Fiber router
func (r *Routes) Register(router fiber.Router, prefix string) {
	g := router.Group(prefix)

	// Metadata endpoint - returns metadata without content bytes
	g.Get("/metadata/*", r.HandleMetadata)

	// Preview endpoints - render HTML content
	g.Get("/preview/:b64HtmlData", r.HandlePreview)
	g.Post("/preview", r.HandlePreviewPost)

	// Stream endpoint
	g.Get("/stream/:outpoint", r.HandleStream)
}

// RegisterContent registers the wildcard content endpoint at the given prefix.
// This is for standalone content servers (e.g., /content/*).
// Separate from Register() because wildcard routes should be registered last.
func (r *Routes) RegisterContent(router fiber.Router, prefix string) {
	g := router.Group(prefix)
	g.Get("/*", r.HandleContent)
}

// HandleContent serves inscription content with directory resolution
// @Summary Get inscription content
// @Description Serve the content of an inscription by outpoint or txid, with directory and SPA support
// @Tags ordfs
// @Produce octet-stream
// @Param path path string true "Outpoint (txid_vout) or txid, optionally with :seq and /filepath"
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

	// Parse the path to extract pointer, seq, and file path
	pp, err := parsePointerPath(path)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Resolve pointer to outpoint
	outpoint, isTxid, err := resolvePointerToOutpoint(pp.Pointer)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Build request
	var req *Request
	if isTxid {
		req = &Request{
			Txid:    &outpoint.Txid,
			Seq:     pp.Seq,
			Content: true,
			Map:     c.QueryBool("map", false),
			Parent:  c.QueryBool("parent", false),
		}
	} else {
		req = &Request{
			Outpoint: outpoint,
			Seq:      pp.Seq,
			Content:  true,
			Map:      c.QueryBool("map", false),
			Parent:   c.QueryBool("parent", false),
		}
	}

	resp, err := r.ordfs.Load(c.Context(), req)
	if err != nil {
		r.logger.Debug("failed to load content", "path", path, "error", err)
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "inscription not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Check if this is a directory (ord-fs/json)
	if resp.ContentType == "ord-fs/json" {
		return r.handleDirectory(c, resp, pp, req.Seq)
	}

	// Not a directory - serve content directly
	return r.sendContentResponse(c, resp, pp.Seq)
}

// handleDirectory handles ord-fs/json directory content
func (r *Routes) handleDirectory(c *fiber.Ctx, resp *Response, pp *pointerPath, seq *int) error {
	// Parse directory JSON
	var directory map[string]string
	if err := json.Unmarshal(resp.Content, &directory); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid directory format",
		})
	}

	// No file path - redirect to index.html (unless raw query param)
	if pp.FilePath == "" {
		if c.Query("raw") != "" {
			return r.sendContentResponse(c, resp, seq)
		}
		// Redirect to index.html
		redirectURL := fmt.Sprintf("%s/index.html", c.Path())
		return c.Redirect(redirectURL)
	}

	// Look up file in directory
	filePointer, exists := directory[pp.FilePath]

	// SPA fallback: if file doesn't exist, try index.html
	if !exists {
		filePointer, exists = directory["index.html"]
		if !exists {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "file not found and no index.html",
			})
		}
	}

	// Load the file
	filePointer = strings.TrimPrefix(filePointer, "ord://")
	fileOutpoint, isTxid, err := resolvePointerToOutpoint(filePointer)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("invalid file pointer: %v", err),
		})
	}

	var fileReq *Request
	if isTxid {
		fileReq = &Request{
			Txid:    &fileOutpoint.Txid,
			Content: true,
			Map:     c.QueryBool("map", false),
		}
	} else {
		fileReq = &Request{
			Outpoint: fileOutpoint,
			Content:  true,
			Map:      c.QueryBool("map", false),
		}
	}

	fileResp, err := r.ordfs.Load(c.Context(), fileReq)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "file not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Files from directory don't have seq tracking
	return r.sendContentResponse(c, fileResp, nil)
}

// sendContentResponse sends a content response with appropriate headers
func (r *Routes) sendContentResponse(c *fiber.Ctx, resp *Response, seq *int) error {
	c.Set("Content-Type", resp.ContentType)

	if resp.Outpoint != nil {
		c.Set("X-Outpoint", resp.Outpoint.OrdinalString())
	}
	if resp.Origin != nil {
		c.Set("X-Origin", resp.Origin.OrdinalString())
	}
	c.Set("X-Ord-Seq", fmt.Sprintf("%d", resp.Sequence))

	// Cache control based on seq
	if seq == nil || *seq == -1 {
		c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else {
		c.Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	if resp.Map != nil {
		c.Set("X-Map", string(resp.Map))
	}

	if resp.Parent != nil {
		c.Set("X-Parent", resp.Parent.OrdinalString())
	}

	// HEAD request - just send headers
	if c.Method() == fiber.MethodHead {
		if resp.ContentLength > 0 {
			c.Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
		}
		return nil
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
// @Router /api/ordfs/metadata/{path} [get]
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
	req.Content = false // Don't load content bytes
	req.Map = true

	resp, err := r.ordfs.Load(c.Context(), req)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Return metadata without content bytes, using OrdinalString for outpoints
	result := fiber.Map{
		"contentType":   resp.ContentType,
		"contentLength": resp.ContentLength,
		"sequence":      resp.Sequence,
	}
	if resp.Outpoint != nil {
		result["outpoint"] = resp.Outpoint.OrdinalString()
	}
	if resp.Origin != nil {
		result["origin"] = resp.Origin.OrdinalString()
	}
	if resp.Map != nil {
		result["map"] = resp.Map
	}
	if resp.Parent != nil {
		result["parent"] = resp.Parent.OrdinalString()
	}
	return c.JSON(result)
}

// HandlePreview renders base64-encoded HTML content
// @Summary Preview HTML content
// @Description Decode and render base64-encoded HTML
// @Tags ordfs
// @Produce html
// @Param b64HtmlData path string true "Base64-encoded HTML content"
// @Success 200 {string} string "HTML content"
// @Failure 400 {object} map[string]string "Bad request"
// @Router /api/ordfs/preview/{b64HtmlData} [get]
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
// @Router /api/ordfs/preview [post]
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

// HandleStream handles streaming content
// @Summary Stream content
// @Description Stream content from an ordinal chain
// @Tags ordfs
// @Produce octet-stream
// @Param outpoint path string true "Outpoint (txid_vout)"
// @Success 200 {file} binary "Streamed content"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Not found"
// @Router /api/ordfs/stream/{outpoint} [get]
func (r *Routes) HandleStream(c *fiber.Ctx) error {
	outpointStr := c.Params("outpoint")
	if outpointStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "outpoint is required",
		})
	}

	outpoint, err := transaction.OutpointFromString(outpointStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid outpoint format",
		})
	}

	// Parse Range header
	var rangeStart, rangeEnd *int64
	rangeHeader := c.Get("Range")
	if rangeHeader != "" {
		if strings.HasPrefix(rangeHeader, "bytes=") {
			rangeParts := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
			if len(rangeParts) == 2 {
				if rangeParts[0] != "" {
					start, err := strconv.ParseInt(rangeParts[0], 10, 64)
					if err == nil {
						rangeStart = &start
					}
				}
				if rangeParts[1] != "" {
					end, err := strconv.ParseInt(rangeParts[1], 10, 64)
					if err == nil {
						rangeEnd = &end
					}
				}
			}
		}
	}

	c.Set("Transfer-Encoding", "chunked")

	streamResp, err := r.ordfs.StreamContent(c.Context(), outpoint, rangeStart, rangeEnd, c.Response().BodyWriter())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if streamResp.ContentType != "" {
		c.Set("Content-Type", streamResp.ContentType)
	}

	if streamResp.Origin != nil {
		c.Set("X-Origin", streamResp.Origin.OrdinalString())
	}

	return nil
}

// pointerPath represents a parsed pointer path with optional seq and file path
type pointerPath struct {
	Pointer  string // raw pointer string (txid or outpoint, without seq)
	Seq      *int   // sequence number (nil if not specified)
	FilePath string // remaining path after pointer (empty if none)
}

// parsePointerPath parses a URL path to extract pointer, optional seq, and file path
// Format: pointer[:seq][/file/path]
// Examples:
//   - abc123_0 -> {Pointer: "abc123_0", Seq: nil, FilePath: ""}
//   - abc123_0:5 -> {Pointer: "abc123_0", Seq: 5, FilePath: ""}
//   - abc123_0:5/style.css -> {Pointer: "abc123_0", Seq: 5, FilePath: "style.css"}
//   - abc123_0:-1/index.html -> {Pointer: "abc123_0", Seq: -1, FilePath: "index.html"}
func parsePointerPath(path string) (*pointerPath, error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	// Split into segments
	segments := strings.Split(path, "/")
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments in path")
	}

	// First segment is pointer[:seq]
	pointerWithSeq := segments[0]

	// Parse pointer and optional seq
	parts := strings.SplitN(pointerWithSeq, ":", 2)
	pointer := parts[0]
	var seq *int

	if len(parts) > 1 {
		seqVal, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid seq value: %s", parts[1])
		}
		seq = &seqVal
	}

	// Remaining segments form the file path
	filePath := ""
	if len(segments) > 1 {
		filePath = strings.Join(segments[1:], "/")
	}

	return &pointerPath{
		Pointer:  pointer,
		Seq:      seq,
		FilePath: filePath,
	}, nil
}

// resolvePointerToOutpoint attempts to parse pointer as either txid or outpoint
// Returns outpoint and whether it was a txid (needs _0 appended)
func resolvePointerToOutpoint(pointer string) (*transaction.Outpoint, bool, error) {
	// Try as outpoint first
	if strings.Contains(pointer, "_") || strings.Contains(pointer, ".") {
		outpoint, err := transaction.OutpointFromString(pointer)
		if err == nil {
			return outpoint, false, nil
		}
	}

	// Try as txid (64 hex chars)
	if len(pointer) == 64 {
		txHash, err := chainhash.NewHashFromHex(pointer)
		if err != nil {
			return nil, false, fmt.Errorf("invalid txid or outpoint: %w", err)
		}
		outpoint := &transaction.Outpoint{
			Txid:  *txHash,
			Index: 0,
		}
		return outpoint, true, nil
	}

	return nil, false, fmt.Errorf("invalid pointer format")
}

// Regex patterns for path parsing
var (
	// Matches txid_vout or txid.vout (outpoint format)
	outpointPattern = regexp.MustCompile(`^([a-fA-F0-9]{64})[_.](\d+)$`)
	// Matches just txid
	txidPattern = regexp.MustCompile(`^([a-fA-F0-9]{64})$`)
)

// parseContentPath parses a content path into a Request (simple version without file path)
// Supported formats:
//   - txid - just a txid, scans outputs for first inscription
//   - txid:seq - txid with sequence
//   - txid_vout - outpoint format
//   - txid_vout:seq - outpoint with sequence number
func parseContentPath(path string) (*Request, error) {
	path = strings.Trim(path, "/")

	// Split on slash - only use first segment for simple parsing
	parts := strings.SplitN(path, "/", 2)
	pointerWithSeq := parts[0]

	// Split pointer and optional seq
	seqParts := strings.SplitN(pointerWithSeq, ":", 2)
	pointer := seqParts[0]

	var seq *int
	if len(seqParts) > 1 {
		seqVal, err := strconv.Atoi(seqParts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid seq value: %s", seqParts[1])
		}
		seq = &seqVal
	}

	req := &Request{Seq: seq}

	// Try outpoint format (txid_vout or txid.vout)
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

	// Try just txid
	if matches := txidPattern.FindStringSubmatch(pointer); matches != nil {
		txid, err := chainhash.NewHashFromHex(matches[1])
		if err != nil {
			return nil, fmt.Errorf("invalid txid: %w", err)
		}
		req.Txid = txid
		return req, nil
	}

	return nil, fmt.Errorf("invalid path format: expected txid or txid_vout")
}
