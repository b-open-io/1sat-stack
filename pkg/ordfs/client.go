package ordfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Loader resolves ordinal requests. Implemented by *Ordfs (embedded) and
// *Client (remote).
type Loader interface {
	Load(ctx context.Context, req *Request) (*Response, error)
}

// Client is a remote Loader backed by a 1sat-stack ORDFS metadata API,
// e.g. https://api.1sat.app/1sat/ordfs. It resolves metadata (current
// outpoint, origin, merged MAP) but does not load content bytes.
type Client struct {
	baseURL string
	client  *http.Client
}

// NewClient creates a remote ORDFS client. A nil client uses http.DefaultClient.
func NewClient(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

func (c *Client) Load(ctx context.Context, req *Request) (*Response, error) {
	if req.Content {
		return nil, fmt.Errorf("remote ordfs client does not load content")
	}

	var pointer string
	switch {
	case req.Outpoint != nil:
		pointer = fmt.Sprintf("%s_%d", req.Outpoint.Txid.String(), req.Outpoint.Index)
	case req.Txid != nil:
		pointer = req.Txid.String()
	default:
		return nil, fmt.Errorf("outpoint or txid required")
	}
	if req.Seq != nil {
		pointer = fmt.Sprintf("%s:%d", pointer, *req.Seq)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/metadata/"+pointer, nil)
	if err != nil {
		return nil, err
	}
	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = httpResp.Body.Close() }()
	if httpResp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ordfs remote metadata/%s: unexpected status %d", pointer, httpResp.StatusCode)
	}

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	var meta struct {
		ContentType   string          `json:"contentType"`
		ContentLength int             `json:"contentLength"`
		Sequence      int             `json:"sequence"`
		Outpoint      string          `json:"outpoint"`
		Origin        string          `json:"origin"`
		Map           json.RawMessage `json:"map"`
		Parent        string          `json:"parent"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse ordfs metadata response: %w", err)
	}

	resp := &Response{
		ContentType:   meta.ContentType,
		ContentLength: meta.ContentLength,
		Sequence:      meta.Sequence,
		Map:           meta.Map,
	}
	if meta.Outpoint != "" {
		if resp.Outpoint, err = transaction.OutpointFromString(meta.Outpoint); err != nil {
			return nil, fmt.Errorf("invalid outpoint in metadata response: %w", err)
		}
	}
	if meta.Origin != "" {
		if resp.Origin, err = transaction.OutpointFromString(meta.Origin); err != nil {
			return nil, fmt.Errorf("invalid origin in metadata response: %w", err)
		}
	}
	if meta.Parent != "" {
		if resp.Parent, err = transaction.OutpointFromString(meta.Parent); err != nil {
			return nil, fmt.Errorf("invalid parent in metadata response: %w", err)
		}
	}
	return resp, nil
}
