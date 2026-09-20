package chaintracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bsv-blockchain/go-sdk/block"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
)

// Client reads block headers from a remote 1sat-stack chaintracks API, e.g.
// https://api.1sat.app/1sat/chaintracks. It speaks the routes this package
// documents — /tip, /height, /network, /header/height/{height},
// /header/hash/{hash}, /headers — which are the go-chaintracks fiber
// handlers as the stack mounts them, directly under /chaintracks with no
// version segment.
//
// go-chaintracks ships its own client, but it requests /v2/tip and
// /v2/header/height/{n}, because that library's reference server groups its
// routes under /v2. Its baseURL is unexported, so the paths cannot be
// adjusted from outside. The unversioned form is what the stack serves and
// what the TypeScript ChaintracksClient in the 1sat SDK already targets, so
// this client matches that rather than the library's.
//
// Client satisfies go-sdk's chaintracker.ChainTracker, so it can be handed
// straight to anything that validates merkle proofs. Headers are immutable
// once written, so those fetched by height are cached for the life of the
// client; the tip and the height are not.
//
// Streaming (/tip/stream, /reorg/stream) is deliberately not implemented:
// nothing in the stack consumes it over HTTP.
type Client struct {
	baseURL string
	client  *http.Client

	mu       sync.RWMutex
	byHeight map[uint32]*Header
}

// DefaultTimeout bounds one header request. Header reads sit in the middle
// of proof validation, so they fail fast rather than hang.
const DefaultTimeout = 10 * time.Second

// ErrNotFound is returned when the service has no header for a height or
// hash (HTTP 404).
var ErrNotFound = errors.New("chaintracks: header not found")

// Header is a block header as the chaintracks routes serve it: the 80-byte
// header fields (hashes as big-endian hex, the form chainhash prints), plus
// the block's height and hash.
type Header struct {
	block.Header
	Height uint32         `json:"height"`
	Hash   chainhash.Hash `json:"hash"`
}

var _ chaintracker.ChainTracker = (*Client)(nil)

// NewClient creates a client for a chaintracks base URL. A nil http client
// gets one with DefaultTimeout.
func NewClient(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:   client,
		byHeight: map[uint32]*Header{},
	}
}

// BaseURL is the chaintracks root this client reads from.
func (c *Client) BaseURL() string { return c.baseURL }

// fetch performs one GET and returns the body of a 200.
func (c *Client) fetch(ctx context.Context, path, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chaintracks %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("chaintracks %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("chaintracks %s: %w", path, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chaintracks %s: unexpected status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// getJSON fetches and decodes a JSON route.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	body, err := c.fetch(ctx, path, "application/json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("chaintracks %s: %w", path, err)
	}
	return nil
}

// Tip returns the current chain tip header. It is never cached.
func (c *Client) Tip(ctx context.Context) (*Header, error) {
	var h Header
	if err := c.getJSON(ctx, "/tip", &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Height returns the current chain height from /height, the cheapest route
// for callers that only want the number.
func (c *Client) Height(ctx context.Context) (uint32, error) {
	var res struct {
		Height uint32 `json:"height"`
	}
	if err := c.getJSON(ctx, "/height", &res); err != nil {
		return 0, err
	}
	return res.Height, nil
}

// Network returns the chain the service tracks ("main", "test").
func (c *Client) Network(ctx context.Context) (string, error) {
	var res struct {
		Network string `json:"network"`
	}
	if err := c.getJSON(ctx, "/network", &res); err != nil {
		return "", err
	}
	return res.Network, nil
}

// HeaderByHeight returns the header at a height, from the cache when it has
// been read before. A height the service does not have returns ErrNotFound.
func (c *Client) HeaderByHeight(ctx context.Context, height uint32) (*Header, error) {
	c.mu.RLock()
	cached := c.byHeight[height]
	c.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	var h Header
	if err := c.getJSON(ctx, fmt.Sprintf("/header/height/%d", height), &h); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.byHeight[height] = &h
	c.mu.Unlock()
	return &h, nil
}

// HeaderByHash returns the header with a block hash, or ErrNotFound.
func (c *Client) HeaderByHash(ctx context.Context, hash *chainhash.Hash) (*Header, error) {
	if hash == nil {
		return nil, fmt.Errorf("chaintracks: nil block hash")
	}
	var h Header
	if err := c.getJSON(ctx, "/header/hash/"+hash.String(), &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// RawHeaders returns count consecutive 80-byte headers starting at height,
// concatenated, as the /headers route serves them.
func (c *Client) RawHeaders(ctx context.Context, height, count uint32) ([]byte, error) {
	raw, err := c.fetch(ctx, fmt.Sprintf("/headers?height=%d&count=%d", height, count), "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if len(raw)%block.HeaderSize != 0 {
		return nil, fmt.Errorf("chaintracks /headers: %d bytes is not a whole number of %d-byte headers", len(raw), block.HeaderSize)
	}
	return raw, nil
}

// IsValidRootForHeight implements chaintracker.ChainTracker: it reads the
// header at height and compares merkle roots. A height the service cannot
// serve is an error, not a false verdict.
func (c *Client) IsValidRootForHeight(ctx context.Context, root *chainhash.Hash, height uint32) (bool, error) {
	h, err := c.HeaderByHeight(ctx, height)
	if err != nil {
		return false, err
	}
	return h.MerkleRoot.IsEqual(root), nil
}

// CurrentHeight implements chaintracker.ChainTracker.
func (c *Client) CurrentHeight(ctx context.Context) (uint32, error) {
	return c.Height(ctx)
}

// GetHeight is CurrentHeight for callers that only want a number, matching
// the signature go-chaintracks' own Chaintracks interface uses. It returns 0
// when the service cannot be reached.
func (c *Client) GetHeight(ctx context.Context) uint32 {
	height, err := c.Height(ctx)
	if err != nil {
		return 0
	}
	return height
}
