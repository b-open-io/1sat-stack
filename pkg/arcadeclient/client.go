// Package arcadeclient is an HTTP client for the external arcade transaction
// broadcast service (https://github.com/bsv-blockchain/arcade). It exposes
// Submit, GetStatus, and Subscribe primitives plus higher-level helpers built
// on top of them.
package arcadeclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// Client is an HTTP client for arcade's transaction API.
type Client struct {
	baseURL       string
	callbackToken string
	http          *http.Client
	logger        *slog.Logger
}

// New constructs a new arcade HTTP client.
//
// baseURL is the arcade endpoint root (e.g. "https://arcade.gorillapool.io").
// callbackToken is registered with arcade on every Submit so SSE can fan
// status updates back to this client. Pass "" if SSE is not used.
//
// If httpClient is nil, a default client without a global timeout is used —
// callers control timeouts via context. SSE consumes long-lived responses
// from this same client, so do not configure http.Client.Timeout on it.
func New(baseURL, callbackToken string, httpClient *http.Client, logger *slog.Logger) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		callbackToken: callbackToken,
		http:          httpClient,
		logger:        logger,
	}
}

// CallbackToken returns the client's default callback token.
func (c *Client) CallbackToken() string {
	return c.callbackToken
}

// Submit posts a serialized BSV transaction to arcade and returns the computed txid.
//
// Arcade returns 202 Accepted with body {"status":"submitted"} and does NOT echo
// the txid; we compute it client-side from rawTx. Track the lifecycle via
// GetStatus or by listening on Subscribe.
func (c *Client) Submit(ctx context.Context, rawTx []byte, opts SubmitOptions) (string, error) {
	txid := ComputeTxid(rawTx)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tx", bytes.NewReader(rawTx))
	if err != nil {
		return "", fmt.Errorf("build submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	c.applySubmitHeaders(req, opts)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit /tx: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("arcade submit returned %d: %s", resp.StatusCode, string(body))
	}

	return txid, nil
}

// GetStatus fetches the current status of a transaction by txid.
// Returns (nil, nil) if arcade has no record of the txid (404).
func (c *Client) GetStatus(ctx context.Context, txid string) (*TransactionStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/tx/"+txid, nil)
	if err != nil {
		return nil, fmt.Errorf("build status request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get /tx/%s: %w", txid, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("arcade get /tx/%s returned %d: %s", txid, resp.StatusCode, string(body))
	}

	var status TransactionStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode status response: %w", err)
	}
	return &status, nil
}

// applySubmitHeaders sets the optional submit headers based on opts and client defaults.
// opts.CallbackToken takes precedence over the client default; pass "" to use the default.
func (c *Client) applySubmitHeaders(req *http.Request, opts SubmitOptions) {
	token := opts.CallbackToken
	if token == "" {
		token = c.callbackToken
	}
	if token != "" {
		req.Header.Set("X-CallbackToken", token)
	}
	if opts.CallbackURL != "" {
		req.Header.Set("X-CallbackUrl", opts.CallbackURL)
	}
	if opts.FullStatusUpdates {
		req.Header.Set("X-FullStatusUpdates", "true")
	}
}

// ComputeTxid returns the hex-encoded txid (display order) of a serialized BSV transaction.
// The txid is sha256d of the raw tx bytes; BSV displays it in reverse byte order from
// the natural hash output.
func ComputeTxid(rawTx []byte) string {
	h1 := sha256.Sum256(rawTx)
	h2 := sha256.Sum256(h1[:])
	reversed := make([]byte, 32)
	for i := 0; i < 32; i++ {
		reversed[31-i] = h2[i]
	}
	return hex.EncodeToString(reversed)
}
