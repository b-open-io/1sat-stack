package ordfs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/httputil"
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
func (r *Routes) Register(router fiber.Router) {
	// Metadata endpoints
	router.Get("/metadata/*", r.HandleMetadata)
	router.Post("/metadata", r.HandleBulkMetadata)

	// Preview endpoints - render HTML content
	router.Get("/preview/:b64HtmlData", r.HandlePreview)
	router.Post("/preview", r.HandlePreviewPost)

	// Stream endpoint
	router.Get("/stream/:outpoint", r.HandleStream)
}

// RegisterContent registers the wildcard content endpoint.
// This is for standalone content servers (e.g., /content/*).
// Separate from Register() because wildcard routes should be registered last.
func (r *Routes) RegisterContent(router fiber.Router) {
	router.Get("/*", r.HandleContent)
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

	loadCtx, loadCancel := context.WithTimeout(c.Context(), ResolveTimeout)
	defer loadCancel()
	resp, err := r.ordfs.Load(loadCtx, req)
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

const maxDirectoryDepth = 8

// handleDirectory handles ord-fs/json directory content with recursive traversal.
// Subdirectory entries pointing to other ord-fs/json inscriptions are followed
// automatically, allowing nested directory trees (e.g., /content/root/refs/api.md).
func (r *Routes) handleDirectory(c *fiber.Ctx, resp *Response, pp *pointerPath, seq *int) error {
	// Parse directory JSON
	var directory map[string]string
	if err := json.Unmarshal(resp.Content, &directory); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid directory format",
		})
	}

	// No file path — redirect to index.html (unless raw query param)
	if pp.FilePath == "" {
		if c.Query("raw") != "" {
			return r.sendContentResponse(c, resp, seq)
		}
		redirectURL := fmt.Sprintf("%s/index.html", c.Path())
		return c.Redirect(redirectURL)
	}

	// Split path into segments for recursive traversal
	segments := strings.Split(pp.FilePath, "/")

	return r.resolveDirectoryPath(c, resp, directory, segments, 0)
}

// resolveDirectoryPath walks a directory tree by path segments.
// If a segment resolves to another ord-fs/json inscription, it recurses.
func (r *Routes) resolveDirectoryPath(
	c *fiber.Ctx,
	dirResp *Response,
	directory map[string]string,
	segments []string,
	depth int,
) error {
	if depth >= maxDirectoryDepth {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "directory nesting too deep",
		})
	}

	segment := segments[0]
	remaining := segments[1:]

	// Look up this segment in the directory
	filePointer, exists := directory[segment]

	// SPA fallback: if not found, try index.html (only for the final segment)
	if !exists && len(remaining) == 0 {
		filePointer, exists = directory["index.html"]
	}
	if !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("'%s' not found in directory", segment),
		})
	}

	// Resolve the pointer to a file
	fileResp, err := r.loadDirectoryEntry(c, dirResp, filePointer)
	if err != nil {
		return err // already an HTTP response
	}

	// If there are more path segments and this entry is a subdirectory, recurse
	if len(remaining) > 0 && fileResp.ContentType == "ord-fs/json" {
		var subdir map[string]string
		if err := json.Unmarshal(fileResp.Content, &subdir); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid subdirectory format",
			})
		}
		return r.resolveDirectoryPath(c, fileResp, subdir, remaining, depth+1)
	}

	// Final segment or non-directory — serve the content
	return r.sendContentResponse(c, fileResp, nil)
}

// loadDirectoryEntry resolves a single directory entry pointer and loads its content.
// Handles relative vout (_N), ord:// prefixes, outpoints, and bare txids.
func (r *Routes) loadDirectoryEntry(
	c *fiber.Ctx,
	dirResp *Response,
	pointer string,
) (*Response, error) {
	pointer = strings.TrimPrefix(pointer, "ord://")

	// Relative vout reference (_N) — sibling output in same transaction
	if vout, ok := parseRelativeVout(pointer); ok {
		if dirResp.Outpoint == nil {
			return nil, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "cannot resolve relative vout — directory outpoint unknown",
			})
		}
		fileResp, err := r.ordfs.Load(c.Context(), &Request{
			Outpoint: &transaction.Outpoint{
				Txid:  dirResp.Outpoint.Txid,
				Index: vout,
			},
			Content: true,
			Map:     c.QueryBool("map", false),
		})
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, c.Status(fiber.StatusNotFound).JSON(fiber.Map{
					"error": fmt.Sprintf("file at vout %d not found", vout),
				})
			}
			return nil, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		return fileResp, nil
	}

	// Absolute outpoint or txid
	outpoint, isTxid, err := resolvePointerToOutpoint(pointer)
	if err != nil {
		return nil, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("invalid file pointer: %v", err),
		})
	}

	var req *Request
	if isTxid {
		req = &Request{Txid: &outpoint.Txid, Content: true, Map: c.QueryBool("map", false)}
	} else {
		req = &Request{Outpoint: outpoint, Content: true, Map: c.QueryBool("map", false)}
	}

	fileCtx, fileCancel := context.WithTimeout(c.Context(), ResolveTimeout)
	defer fileCancel()
	fileResp, err := r.ordfs.Load(fileCtx, req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "file not found",
			})
		}
		return nil, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return fileResp, nil
}

// sendContentResponse sends a content response with appropriate headers
func (r *Routes) sendContentResponse(c *fiber.Ctx, resp *Response, seq *int) error {
	c.Set("Content-Type", resp.ContentType)

	if resp.Outpoint != nil {
		c.Set("X-Outpoint", resp.Outpoint.String())
	}
	if resp.Origin != nil {
		c.Set("X-Origin", resp.Origin.String())
	}
	c.Set("X-Ord-Seq", fmt.Sprintf("%d", resp.Sequence))

	if seq != nil && *seq == -1 {
		httputil.SetNoStore(c)
	} else {
		httputil.SetImmutable(c)
	}

	if resp.Map != nil {
		c.Set("X-Map", string(resp.Map))
	}

	if resp.Parent != nil {
		c.Set("X-Parent", resp.Parent.String())
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
// @Router /metadata/{path} [get]
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

	metaCtx, metaCancel := context.WithTimeout(c.Context(), ResolveTimeout)
	defer metaCancel()
	resp, err := r.ordfs.Load(metaCtx, req)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	httputil.SetNoStore(c)
	result := fiber.Map{
		"contentType":   resp.ContentType,
		"contentLength": resp.ContentLength,
		"sequence":      resp.Sequence,
	}
	if resp.Outpoint != nil {
		result["outpoint"] = resp.Outpoint.String()
	}
	if resp.Origin != nil {
		result["origin"] = resp.Origin.String()
	}
	if resp.Map != nil {
		result["map"] = resp.Map
	}
	if resp.Parent != nil {
		result["parent"] = resp.Parent.String()
	}
	return c.JSON(result)
}

// HandleBulkMetadata returns metadata for multiple outpoints
// @Summary Bulk metadata lookup
// @Description Get metadata for multiple outpoints in a single request
// @Tags ordfs
// @Accept json
// @Produce json
// @Param body body object true "Outpoints to look up" SchemaExample({"outpoints":["txid_0","txid_1"]})
// @Success 200 {object} map[string]interface{} "Map of outpoint to metadata (null if not found)"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 500 {object} map[string]string "Internal error"
// @Router /metadata [post]
func (r *Routes) HandleBulkMetadata(c *fiber.Ctx) error {
	var body struct {
		Outpoints []string `json:"outpoints"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid JSON body",
		})
	}

	if len(body.Outpoints) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "outpoints array is required",
		})
	}
	if len(body.Outpoints) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "maximum 100 outpoints per request",
		})
	}

	type result struct {
		outpoint string
		data     any
		err      error
	}

	results := make([]result, len(body.Outpoints))
	var wg sync.WaitGroup

	for i, op := range body.Outpoints {
		wg.Add(1)
		go func(idx int, outpointStr string) {
			defer wg.Done()

			req, err := parseContentPath(outpointStr)
			if err != nil {
				results[idx] = result{outpoint: outpointStr, err: fmt.Errorf("invalid outpoint: %w", err)}
				return
			}
			req.Content = false
			req.Map = true

			bulkCtx, bulkCancel := context.WithTimeout(c.Context(), ResolveTimeout)
			defer bulkCancel()
			resp, err := r.ordfs.Load(bulkCtx, req)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					results[idx] = result{outpoint: outpointStr, data: nil}
					return
				}
				results[idx] = result{outpoint: outpointStr, err: err}
				return
			}

			entry := fiber.Map{
				"contentType":   resp.ContentType,
				"contentLength": resp.ContentLength,
				"sequence":      resp.Sequence,
			}
			if resp.Origin != nil {
				entry["origin"] = resp.Origin.String()
			}
			if resp.Outpoint != nil {
				entry["outpoint"] = resp.Outpoint.String()
			}
			if resp.Map != nil {
				entry["map"] = resp.Map
			}
			results[idx] = result{outpoint: outpointStr, data: entry}
		}(i, op)
	}

	wg.Wait()

	httputil.SetNoStore(c)
	response := make(fiber.Map, len(results))
	for _, r := range results {
		if r.err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fmt.Sprintf("failed to load %s: %v", r.outpoint, r.err),
			})
		}
		response[r.outpoint] = r.data
	}

	return c.JSON(response)
}

// HandlePreview renders base64-encoded HTML content
// @Summary Preview HTML content
// @Description Decode and render base64-encoded HTML
// @Tags ordfs
// @Produce html
// @Param b64HtmlData path string true "Base64-encoded HTML content"
// @Success 200 {string} string "HTML content"
// @Failure 400 {object} map[string]string "Bad request"
// @Router /preview/{b64HtmlData} [get]
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
// @Router /preview [post]
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
// @Router /stream/{outpoint} [get]
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
		c.Set("X-Origin", streamResp.Origin.String())
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

// relativeVoutPattern matches _N (e.g., "_0", "_8") — a relative reference
// to a sibling output in the same transaction as the directory inscription.
var relativeVoutPattern = regexp.MustCompile(`^_(\d+)$`)

// parseRelativeVout checks if a string is a relative vout reference (_N)
// used in ord-fs/json directories to reference sibling outputs.
func parseRelativeVout(pointer string) (uint32, bool) {
	m := relativeVoutPattern.FindStringSubmatch(pointer)
	if m == nil {
		return 0, false
	}
	vout, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(vout), true
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
