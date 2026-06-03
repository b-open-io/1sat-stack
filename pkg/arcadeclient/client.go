// Package arcadeclient is an HTTP client for the external arcade transaction
// broadcast service (https://github.com/bsv-blockchain/arcade). It exposes
// Submit, GetStatus, and Subscribe primitives plus higher-level helpers built
// on top of them.
package arcadeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// defaultSSEIdleTimeout is the default read-idle window on the /events stream.
// Arcade sends a keepalive comment every 15s; this gives ~2 missed pings before
// we force a reconnect.
const defaultSSEIdleTimeout = 30 * time.Second

// defaultResponseHeaderTimeout caps how long http.Client.Do will wait for
// response headers after the request is fully sent. Without this the SSE
// consumer can block indefinitely if arcade accepts the TCP connection but
// never returns HTTP headers (observed during arcade restart windows).
// Healthy responses are sub-second; 5s is comfortably above normal noise.
const defaultResponseHeaderTimeout = 5 * time.Second

// Client is an HTTP client for arcade's transaction API.
type Client struct {
	baseURL        string
	callbackToken  string
	http           *http.Client
	logger         *slog.Logger
	sseIdleTimeout time.Duration
}

// New constructs a new arcade HTTP client.
//
// baseURL is the arcade endpoint root (e.g. "https://arcade.gorillapool.io").
// callbackToken is registered with arcade on every Submit so SSE can fan
// status updates back to this client. Pass "" if SSE is not used.
//
// If httpClient is nil, a default client is constructed with a custom
// transport that sets ResponseHeaderTimeout (so initial connect can't hang
// forever) but no overall http.Client.Timeout — SSE consumes long-lived
// response bodies. Callers control overall request lifetime via context.
func New(baseURL, callbackToken string, httpClient *http.Client, logger *slog.Logger) *Client {
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = defaultResponseHeaderTimeout
		httpClient = &http.Client{Transport: transport}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		callbackToken:  callbackToken,
		http:           httpClient,
		logger:         logger,
		sseIdleTimeout: defaultSSEIdleTimeout,
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
	txid, err := ComputeTxid(rawTx)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tx", bytes.NewReader(rawTx))
	if err != nil {
		return "", fmt.Errorf("build submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	c.applySubmitHeaders(req, opts)

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("arcade submit transport error", "txid", txid, "err", err)
		return "", fmt.Errorf("submit /tx: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.logger.Warn("arcade submit non-success status",
			"txid", txid, "http_status", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("arcade submit returned %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Info("arcade submit accepted", "txid", txid, "http_status", resp.StatusCode, "size", len(rawTx))
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

// ComputeTxid returns the canonical hex-encoded txid of a serialized BSV
// transaction. rawTx may be standard or Extended Format; the SDK parses both
// and TxID is computed over the canonical form.
func ComputeTxid(rawTx []byte) (string, error) {
	tx, err := transaction.NewTransactionFromBytes(rawTx)
	if err != nil {
		return "", fmt.Errorf("parse tx for txid: %w", err)
	}
	return tx.TxID().String(), nil
}
