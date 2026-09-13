package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// ErrServerDown is returned when the admin API cannot be reached at all,
// which the command takes to mean the server is not running.
var ErrServerDown = errors.New("server not reachable")

// AdminClient talks to the running server's admin API with the shared
// secret (auth.api_key) read from the config store. The server accepts it
// in the X-Api-Key header; in local mode (auth.allow_unauthenticated) the
// header is simply ignored.
type AdminClient struct {
	base   string
	apiKey string
	http   *http.Client
}

func NewAdminClient(base, apiKey string) *AdminClient {
	return &AdminClient{base: base, apiKey: apiKey, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *AdminClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-Api-Key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrServerDown, err)
		}
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			return nil, fmt.Errorf("%w: %v", ErrServerDown, err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("admin api %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(out))
	}
	return out, nil
}

// UpdateConfig writes keys through PUT /config. An empty value deletes the
// key, matching the admin UI's contract.
func (c *AdminClient) UpdateConfig(ctx context.Context, updates map[string]string) error {
	_, err := c.do(ctx, http.MethodPut, "/config", updates)
	return err
}

// Restart asks the server to restart itself (POST /restart).
func (c *AdminClient) Restart(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/restart", nil)
	return err
}

// Enqueue places a txid or outpoint on the named store queue by adding it to
// the q:<name> sorted set through the admin data API, with the zero score
// the event bridge uses for live events.
func (c *AdminClient) Enqueue(ctx context.Context, queue, member string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/data/zset/add/q:"+queue, map[string]any{"member": member, "score": 0})
}
