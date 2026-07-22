package bsv21

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPOwnerClient calls a remote index service's owner routes to sync a fee
// address and read its balance over HTTP, letting bsv21 run without an
// in-process index. baseURL points at the owner mount, e.g.
// https://api.1sat.app/1sat/owner. It satisfies OwnerSyncer, and its Balance
// method satisfies BalanceLookup.
type HTTPOwnerClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPOwnerClient creates an HTTP-backed owner client. A nil client uses
// http.DefaultClient.
func NewHTTPOwnerClient(baseURL string, client *http.Client) *HTTPOwnerClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPOwnerClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

// Sync drains the remote owner sync stream (GET /sync?owner=<addr>). The index
// service triggers its background sync once the stream is exhausted, so reading
// to EOF is what kicks the sync off.
func (c *HTTPOwnerClient) Sync(ctx context.Context, owner string) error {
	u := c.baseURL + "/sync?owner=" + url.QueryEscape(owner)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("owner remote sync: unexpected status %d", resp.StatusCode)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return err
	}
	return nil
}

// Balance returns unspent satoshis for the address (GET /:owner/balance).
func (c *HTTPOwnerClient) Balance(ctx context.Context, address string) (int64, error) {
	u := c.baseURL + "/" + url.PathEscape(address) + "/balance"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("owner remote balance: unexpected status %d", resp.StatusCode)
	}
	var br struct {
		Balance uint64 `json:"balance"`
		Count   int    `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		return 0, err
	}
	return int64(br.Balance), nil
}
